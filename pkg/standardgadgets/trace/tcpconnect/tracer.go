// Copyright 2022 The Inspektor Gadget authors
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

package standard

import (
	"strings"

	"github.com/lato333/inspektor-gadget/pkg/gadgets/trace/tcpconnect/tracer"
	"github.com/lato333/inspektor-gadget/pkg/gadgets/trace/tcpconnect/types"
	"github.com/lato333/inspektor-gadget/pkg/standardgadgets/trace"
)

func NewTracer(config *tracer.Config, eventCallback func(types.Event)) (*trace.StandardTracer[types.Event], error) {
	prepareLine := func(line string) string {
		// "Hack" to avoid changing the BCC tool implementation
		line = strings.ReplaceAll(line, `"ip"`, `"ipversion"`)
		line = strings.ReplaceAll(line, `"type"`, `"operation"`)
		return line
	}

	standardConfig := &trace.StandardTracerConfig[types.Event]{
		ScriptName:    "tcpconnect",
		EventCallback: eventCallback,
		PrepareLine:   prepareLine,
		BaseEvent:     types.Base,
		MntnsMap:      config.MountnsMap,
	}

	return trace.NewStandardTracer(standardConfig)
}
