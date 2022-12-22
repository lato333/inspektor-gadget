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

package types

import (
	"fmt"
	"time"

	"github.com/docker/go-units"
	"github.com/lato333/inspektor-gadget/pkg/columns"
	eventtypes "github.com/lato333/inspektor-gadget/pkg/types"
)

var SortByDefault = []string{"-runtime", "-runcount"}

type Stats struct {
	eventtypes.CommonData
	ProgramID          uint32     `json:"progid" column:"progid"`
	Type               string     `json:"type,omitempty" column:"type"`
	Name               string     `json:"name,omitempty" column:"name"`
	Pids               []*PidInfo `json:"pids,omitempty" column:"pid"`
	CurrentRuntime     int64      `json:"currentRuntime,omitempty" column:"runtime,order:1001,align:right"`
	CurrentRunCount    uint64     `json:"currentRunCount,omitempty" column:"runcount,order:1002,width:10"`
	CumulativeRuntime  int64      `json:"cumulRuntime,omitempty" column:"cumulruntime,order:1003,hide"`
	CumulativeRunCount uint64     `json:"cumulRunCount,omitempty" column:"cumulruncount,order:1004,hide"`
	TotalRuntime       int64      `json:"totalRuntime,omitempty" column:"totalruntime,order:1005,align:right,hide"`
	TotalRunCount      uint64     `json:"totalRunCount,omitempty" column:"totalRunCount,order:1006,align:right,hide"`
	MapMemory          uint64     `json:"mapMemory,omitempty" column:"mapmemory,order:1007,align:right"`
	MapCount           uint32     `json:"mapCount,omitempty" column:"mapcount,order:1008"`
}

func GetColumns() *columns.Columns[Stats] {
	cols := columns.MustCreateColumns[Stats]()

	col, _ := cols.GetColumn("namespace")
	col.Visible = false
	col, _ = cols.GetColumn("pod")
	col.Visible = false
	col, _ = cols.GetColumn("container")
	col.Visible = false

	cols.MustSetExtractor("pid", func(stats *Stats) (ret string) {
		if len(stats.Pids) > 0 {
			return fmt.Sprint(stats.Pids[0].Pid)
		}
		return ""
	})
	cols.MustAddColumn(columns.Column[Stats]{
		Name:     "comm",
		MaxWidth: 16,
		Visible:  true,
		Order:    1000,
		Extractor: func(stats *Stats) string {
			if len(stats.Pids) > 0 {
				return fmt.Sprint(stats.Pids[0].Comm)
			}
			return ""
		},
	})
	cols.MustSetExtractor("runtime", func(stats *Stats) (ret string) {
		return fmt.Sprint(time.Duration(stats.CurrentRuntime))
	})
	cols.MustSetExtractor("totalruntime", func(stats *Stats) (ret string) {
		return fmt.Sprint(time.Duration(stats.TotalRuntime))
	})
	cols.MustSetExtractor("cumulruntime", func(stats *Stats) (ret string) {
		return fmt.Sprint(time.Duration(stats.CumulativeRuntime))
	})
	cols.MustSetExtractor("mapmemory", func(stats *Stats) (ret string) {
		return fmt.Sprint(units.BytesSize(float64(stats.MapMemory)))
	})

	return cols
}

type PidInfo struct {
	Pid  uint32 `json:"pid,omitempty"`
	Comm string `json:"comm,omitempty"`
}
