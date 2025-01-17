//go:build linux
// +build linux

// Copyright 2019-2022 The Inspektor Gadget authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tracer

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/perf"
	"github.com/lato333/inspektor-gadget/pkg/gadgets"
	"github.com/lato333/inspektor-gadget/pkg/gadgets/traceloop/types"
	eventtypes "github.com/lato333/inspektor-gadget/pkg/types"
	libseccomp "github.com/seccomp/libseccomp-golang"
	log "github.com/sirupsen/logrus"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type syscall_event_t -type syscall_event_cont_t -target ${TARGET} -cc clang traceloop ./bpf/traceloop.bpf.c -- -I./bpf/ -I../../../${TARGET}

// These variables must match content of traceloop.h.
var (
	useNullByteLength        uint64 = 0x0fffffffffffffff
	useRetAsParamLength      uint64 = 0x0ffffffffffffffe
	useArgIndexAsParamLength uint64 = 0x0ffffffffffffff0
	paramProbeAtExitMask     uint64 = 0xf000000000000000

	syscallEventTypeEnter uint8 = 0
	syscallEventTypeExit  uint8 = 1
)

// This should match traceloop.h define SYSCALL_ARGS.
var syscallArgs uint8 = 6

var (
	syscallsOnce         sync.Once
	syscallsDeclarations map[string]syscallDeclaration
)

type containerRingReader struct {
	perfReader *perf.Reader
	// This is indexed by ring index, i.e. CPU number.
	previousHeadPos []uint64
	mntnsID         uint64
}

type Tracer struct {
	enricher gadgets.DataEnricher

	innerMapSpec *ebpf.MapSpec

	objs      traceloopObjects
	enterLink link.Link
	exitLink  link.Link

	// Same comment than above, this map is designed to handle parallel access.
	// The keys of this map are containerID.
	readers sync.Map
}

type syscallEvent struct {
	timestamp uint64
	typ       uint8
	contNr    uint8
	cpu       uint16
	id        uint16
	pid       uint32
	comm      string
	args      []uint64
	mountNsID uint64
	retval    int
}

type syscallEventContinued struct {
	timestamp uint64
	index     uint8
	param     string
}

func NewTracer(enricher gadgets.DataEnricher) (*Tracer, error) {
	t := &Tracer{
		enricher: enricher,
	}

	spec, err := loadTraceloop()
	if err != nil {
		return nil, fmt.Errorf("loading ebpf program: %w", err)
	}

	syscallsOnce.Do(func() {
		syscallsDeclarations, err = gatherSyscallsDeclarations()
	})
	if err != nil {
		return nil, fmt.Errorf("gathering syscall definitions: %w", err)
	}

	// Fill the syscall map with specific syscall signatures.
	syscallsMapSpec := spec.Maps["syscalls"]
	for name, def := range syscallDefs {
		nr, err := libseccomp.GetSyscallFromName(name)
		if err != nil {
			return nil, fmt.Errorf("getting syscall number of %q: %w", name, err)
		}

		// We need to do so to avoid taking each time the same address.
		def := def
		syscallsMapSpec.Contents = append(syscallsMapSpec.Contents, ebpf.MapKV{
			Key:   uint64(nr),
			Value: def,
		})
	}

	if err := spec.LoadAndAssign(&t.objs, nil); err != nil {
		return nil, fmt.Errorf("loading ebpf program: %w", err)
	}

	defer func() {
		// So we are sure to clean everything before exiting in case of error.
		if err != nil {
			t.Stop()
		}
	}()

	t.enterLink, err = link.AttachRawTracepoint(link.RawTracepointOptions{
		Name:    "sys_enter",
		Program: t.objs.IgTraceloopE,
	})
	if err != nil {
		return nil, fmt.Errorf("opening enter tracepoint: %w", err)
	}

	t.exitLink, err = link.AttachRawTracepoint(link.RawTracepointOptions{
		Name:    "sys_exit",
		Program: t.objs.IgTraceloopX,
	})
	if err != nil {
		return nil, fmt.Errorf("opening exit tracepoint: %w", err)
	}

	t.innerMapSpec = spec.Maps["map_of_perf_buffers"].InnerMap

	return t, nil
}

func (t *Tracer) Stop() {
	t.enterLink = gadgets.CloseLink(t.enterLink)
	t.exitLink = gadgets.CloseLink(t.exitLink)

	t.readers.Range(func(key, _ any) bool {
		t.Delete(key.(string))

		return true
	})

	t.objs.Close()
}

func (t *Tracer) Attach(containerID string, mntnsID uint64) error {
	innerBufferSpec := t.innerMapSpec.Copy()
	innerBufferSpec.Name = fmt.Sprintf("perf_buffer_%d", mntnsID)

	// 1. Create inner Map as perf buffer.
	innerBuffer, err := ebpf.NewMap(innerBufferSpec)
	if err != nil {
		return fmt.Errorf("error creating inner map: %w", err)
	}

	// 2. Use this inner Map to create the perf reader.
	perfReader, err := perf.NewReaderWithOptions(innerBuffer, gadgets.PerfBufferPages*os.Getpagesize(), perf.ReaderOptions{})
	if err != nil {
		innerBuffer.Close()

		return fmt.Errorf("error creating perf ring buffer: %w", err)
	}

	// 3. Add the inner map's file descriptor to outer map.
	err = t.objs.MapOfPerfBuffers.Put(mntnsID, innerBuffer)
	if err != nil {
		innerBuffer.Close()
		perfReader.Close()

		return fmt.Errorf("error adding perf buffer to map with mntnsID %d: %w", mntnsID, err)
	}

	t.readers.Store(containerID, &containerRingReader{
		perfReader:      perfReader,
		previousHeadPos: make([]uint64, getRingsNumber(perfReader)),
		mntnsID:         mntnsID,
	})

	return nil
}

func (t *Tracer) Read(containerID string) ([]*types.Event, error) {
	syscallContinuedEventsMap := make(map[uint64][]*syscallEventContinued)
	syscallEnterEventsMap := make(map[uint64][]*syscallEvent)
	syscallExitEventsMap := make(map[uint64][]*syscallEvent)
	events := make([]*types.Event, 0)

	r, ok := t.readers.Load(containerID)
	if !ok {
		return nil, fmt.Errorf("no perf reader for %q", containerID)
	}

	reader, ok := r.(*containerRingReader)
	if !ok {
		return nil, errors.New("the map should only contain *containerRingReader")
	}

	if reader.perfReader == nil {
		log.Infof("reader for %v is nil, it was surely detached", containerID)

		return nil, nil
	}

	err := readOverWritable(reader, func(record perf.Record, size uint32) error {
		var sysEvent *traceloopSyscallEventT
		var sysEventCont *traceloopSyscallEventContT

		switch uintptr(size) {
		case alignSize(unsafe.Sizeof(*sysEvent)):
			sysEvent = (*traceloopSyscallEventT)(unsafe.Pointer(&record.RawSample[0]))

			event := &syscallEvent{
				timestamp: sysEvent.Timestamp,
				typ:       sysEvent.Typ,
				contNr:    sysEvent.ContNr,
				cpu:       sysEvent.Cpu,
				id:        sysEvent.Id,
				pid:       sysEvent.Pid,
				comm:      gadgets.FromCString(sysEvent.Comm[:]),
				mountNsID: reader.mntnsID,
			}

			var typeMap *map[uint64][]*syscallEvent
			switch event.typ {
			case syscallEventTypeEnter:
				event.args = make([]uint64, syscallArgs)
				for i := uint8(0); i < syscallArgs; i++ {
					event.args[i] = sysEvent.Args[i]
				}

				typeMap = &syscallEnterEventsMap
			case syscallEventTypeExit:
				// In the C structure, args is an array of uint64.
				// But in this particular case, we used it to store a C long, i.e. the
				// syscall return value, so it is safe to cast it to golang int.
				event.retval = int(sysEvent.Args[0])

				typeMap = &syscallExitEventsMap
			default:
				// Rather than returning an error, we skip this event.
				// Indeed, I suspect this is caused because we copy the buffer while
				// it is being written, so we will get uncomplete data, thus it is
				// better to skip this event.
				log.Debugf("type %d is not a valid type for syscallEvent, received data are: %v", event.typ, record.RawSample)
				return nil
			}

			if _, ok := (*typeMap)[event.timestamp]; !ok {
				(*typeMap)[event.timestamp] = make([]*syscallEvent, 0)
			}

			(*typeMap)[event.timestamp] = append((*typeMap)[event.timestamp], event)
		case alignSize(unsafe.Sizeof(*sysEventCont)):
			sysEventCont = (*traceloopSyscallEventContT)(unsafe.Pointer(&record.RawSample[0]))

			event := &syscallEventContinued{
				timestamp: sysEventCont.Timestamp,
				index:     sysEventCont.Index,
			}

			if sysEventCont.Failed != 0 {
				event.param = "(Failed to dereference pointer)"
			} else if sysEventCont.Length == useNullByteLength {
				// 0 byte at [C.PARAM_LENGTH - 1] is enforced in BPF code
				event.param = gadgets.FromCString(sysEventCont.Param[:])
			} else {
				event.param = gadgets.FromCStringN(sysEventCont.Param[:], int(sysEventCont.Length))
			}

			// Remove all non unicode character from the string.
			event.param = strconv.Quote(event.param)

			_, ok := syscallContinuedEventsMap[event.timestamp]
			if !ok {
				// Just create a 0 elements slice for the moment, the ContNr will be
				// checked later.
				syscallContinuedEventsMap[event.timestamp] = make([]*syscallEventContinued, 0)
			}

			syscallContinuedEventsMap[event.timestamp] = append(syscallContinuedEventsMap[event.timestamp], event)
		default:
			log.Debugf("size %d does not correspond to any expected element, which are %d and %d; received data are: %v", size, unsafe.Sizeof(sysEvent), unsafe.Sizeof(sysEventCont), record.RawSample)
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, perf.ErrClosed) {
			// nothing to do, we're done
			return nil, nil
		}

		return nil, fmt.Errorf("reading backward over writable perf ring buffer: %w", err)
	}

	// Let's try to publish the events we gathered.
	for enterTimestamp, enterTimestampEvents := range syscallEnterEventsMap {
		for _, enterEvent := range enterTimestampEvents {
			syscallName, err := syscallGetName(enterEvent.id)
			if err != nil {
				return nil, fmt.Errorf("getting name of syscall number %d: %w", enterEvent.id, err)
			}

			event := &types.Event{
				Event: eventtypes.Event{
					Type: eventtypes.NORMAL,
				},
				Timestamp: enterTimestamp,
				CPU:       enterEvent.cpu,
				Pid:       enterEvent.pid,
				Comm:      enterEvent.comm,
				MountNsID: enterEvent.mountNsID,
				Syscall:   syscallName,
			}

			syscallDeclaration, err := getSyscallDeclaration(syscallsDeclarations, event.Syscall)
			if err != nil {
				return nil, fmt.Errorf("getting syscall definition")
			}

			parametersNumber := syscallDeclaration.getParameterCount()
			event.Parameters = make([]types.SyscallParam, parametersNumber)
			log.Debugf("\tevent parametersNumber: %d", parametersNumber)

			for i := uint8(0); i < parametersNumber; i++ {
				paramName, err := syscallDeclaration.getParameterName(i)
				if err != nil {
					return nil, fmt.Errorf("getting syscall parameter name: %w", err)
				}
				log.Debugf("\t\tevent paramName: %q", paramName)

				paramValue := fmt.Sprintf("%d", enterEvent.args[i])
				log.Debugf("\t\tevent paramValue: %q", paramValue)

				for _, syscallContEvent := range syscallContinuedEventsMap[enterTimestamp] {
					if syscallContEvent.index == i {
						paramValue = syscallContEvent.param
						log.Debugf("\t\t\tevent paramValue: %q", paramValue)

						break
					}
				}

				event.Parameters[i] = types.SyscallParam{
					Name:  paramName,
					Value: paramValue,
				}
			}

			delete(syscallContinuedEventsMap, enterTimestamp)

			// There is no exit event for exit(), exit_group() and rt_sigreturn().
			if event.Syscall == "exit" || event.Syscall == "exit_group" || event.Syscall == "rt_sigreturn" {
				delete(syscallEnterEventsMap, enterTimestamp)

				if t.enricher != nil {
					t.enricher.Enrich(&event.CommonData, event.MountNsID)
				}

				log.Debugf("%v", event)
				events = append(events, event)

				continue
			}

			exitTimestampEvents, ok := syscallExitEventsMap[enterTimestamp]
			if !ok {
				log.Errorf("no exit event for timestamp %d", enterTimestamp)

				continue
			}

			for _, exitEvent := range exitTimestampEvents {
				if enterEvent.id != exitEvent.id || enterEvent.pid != exitEvent.pid {
					continue
				}

				event.Retval = exitEvent.retval

				delete(syscallEnterEventsMap, enterTimestamp)
				delete(syscallExitEventsMap, enterTimestamp)

				if t.enricher != nil {
					t.enricher.Enrich(&event.CommonData, event.MountNsID)
				}
				log.Debugf("%v", event)
				events = append(events, event)

				break
			}
		}
	}

	log.Debugf("len(events): %d; len(syscallEnterEventsMap): %d; len(syscallExitEventsMap): %d; len(syscallContinuedEventsMap): %d\n", len(events), len(syscallEnterEventsMap), len(syscallExitEventsMap), len(syscallContinuedEventsMap))

	// For strange reason, even though we use the same timestamp for enter and
	// exit events, it is possible there are some incomplete events (i.e. enter event
	// without exit and vice versa).
	// Rather than dropping them, we just add them to the events to be published
	// but they will be incomplete.
	// One possible reason would be that the buffer is full and so it only remains
	// some exit events and not the corresponding enter/
	for _, enterTimestampEvents := range syscallEnterEventsMap {
		for enterTimestamp, enterEvent := range enterTimestampEvents {
			syscallName, err := syscallGetName(enterEvent.id)
			if err != nil {
				// It is best effort, so just long and continue in case of troubles.
				log.Errorf("incomplete enter event: getting name of syscall number %d: %v", enterEvent.id, err)

				continue
			}

			incompleteEnterEvent := &types.Event{
				Event: eventtypes.Event{
					Type: eventtypes.NORMAL,
				},
				Timestamp: uint64(enterTimestamp),
				CPU:       enterEvent.cpu,
				Pid:       enterEvent.pid,
				Comm:      enterEvent.comm,
				MountNsID: enterEvent.mountNsID,
				Syscall:   syscallName,
			}

			if t.enricher != nil {
				t.enricher.Enrich(&incompleteEnterEvent.CommonData, incompleteEnterEvent.MountNsID)
			}

			events = append(events, incompleteEnterEvent)

			log.Debugf("enterEvent(%q): %v\n", syscallName, enterEvent)
		}
	}

	for _, exitTimestampEvents := range syscallExitEventsMap {
		for exitTimestamp, exitEvent := range exitTimestampEvents {
			syscallName, err := syscallGetName(exitEvent.id)
			if err != nil {
				log.Errorf("incomplete exit event: getting name of syscall number %d: %v", exitEvent.id, err)

				continue
			}

			incompleteExitEvent := &types.Event{
				Event: eventtypes.Event{
					Type: eventtypes.NORMAL,
				},
				Timestamp: uint64(exitTimestamp),
				CPU:       exitEvent.cpu,
				Pid:       exitEvent.pid,
				Comm:      exitEvent.comm,
				MountNsID: exitEvent.mountNsID,
				Syscall:   syscallName,
				Retval:    exitEvent.retval,
			}

			if t.enricher != nil {
				t.enricher.Enrich(&incompleteExitEvent.CommonData, incompleteExitEvent.MountNsID)
			}

			events = append(events, incompleteExitEvent)

			log.Debugf("exitEvent(%q): %v\n", syscallName, exitEvent)
		}
	}

	// Sort all events by ascending timestamp.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})

	return events, nil
}

func (t *Tracer) Detach(mntnsID uint64) error {
	err := t.objs.MapOfPerfBuffers.Delete(mntnsID)
	if err != nil {
		return fmt.Errorf("error removing perf buffer from map with mntnsID %d", mntnsID)
	}

	return nil
}

func (t *Tracer) Delete(containerID string) error {
	r, ok := t.readers.LoadAndDelete(containerID)
	if !ok {
		return fmt.Errorf("no reader for containerID %s", containerID)
	}

	reader := r.(*containerRingReader)
	err := reader.perfReader.Close()
	reader.perfReader = nil

	return err
}
