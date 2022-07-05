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

package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kinvolk/inspektor-gadget/cmd/kubectl-gadget/utils"
	"github.com/kinvolk/inspektor-gadget/pkg/gadgets/execsnoop/types"
	eventtypes "github.com/kinvolk/inspektor-gadget/pkg/types"

	"github.com/spf13/cobra"
)

var execsnoopCmd = &cobra.Command{
	Use:   "exec",
	Short: "Trace new processes",
	RunE: func(cmd *cobra.Command, args []string) error {
		// print header
		switch params.OutputMode {
		case utils.OutputModeCustomColumns:
			fmt.Println(getCustomExecsnoopColsHeader(params.CustomColumns))
		case utils.OutputModeColumns:
			fmt.Printf("%-16s %-16s %-16s %-16s %-16s %-6s %-6s %3s %s\n",
				"NODE", "NAMESPACE", "POD", "CONTAINER",
				"PCOMM", "PID", "PPID", "RET", "ARGS")
		}

		config := &utils.TraceConfig{
			GadgetName:       "execsnoop",
			Operation:        "start",
			TraceOutputMode:  "Stream",
			TraceOutputState: "Started",
			CommonFlags:      &params,
		}

		err := utils.RunTraceAndPrintStream(config, execsnoopTransformLine)
		if err != nil {
			return utils.WrapInErrRunGadget(err)
		}

		return nil
	},
}

func init() {
	TraceCmd.AddCommand(execsnoopCmd)
	utils.AddCommonFlags(execsnoopCmd, &params)
}

// execsnoopTransformLine is called to transform an event to columns
// format according to the parameters
func execsnoopTransformLine(line string) string {
	var sb strings.Builder
	var e types.Event

	if err := json.Unmarshal([]byte(line), &e); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s", utils.WrapInErrUnmarshalOutput(err, line))
		return ""
	}

	if e.Type != eventtypes.NORMAL {
		utils.ManageSpecialEvent(e.Event, params.Verbose)
		return ""
	}

	switch params.OutputMode {
	case utils.OutputModeColumns:
		sb.WriteString(fmt.Sprintf("%-16s %-16s %-16s %-16s %-16s %-6d %-6d %3d",
			e.Node, e.Namespace, e.Pod, e.Container,
			e.Comm, e.Pid, e.Ppid, e.Retval))

		for _, arg := range e.Args {
			sb.WriteString(" " + arg)
		}
	case utils.OutputModeCustomColumns:
		for _, col := range params.CustomColumns {
			switch col {
			case "node":
				sb.WriteString(fmt.Sprintf("%-16s", e.Node))
			case "namespace":
				sb.WriteString(fmt.Sprintf("%-16s", e.Namespace))
			case "pod":
				sb.WriteString(fmt.Sprintf("%-16s", e.Pod))
			case "container":
				sb.WriteString(fmt.Sprintf("%-16s", e.Container))
			case "pcomm":
				sb.WriteString(fmt.Sprintf("%-16s", e.Comm))
			case "pid":
				sb.WriteString(fmt.Sprintf("%-6d", e.Pid))
			case "ppid":
				sb.WriteString(fmt.Sprintf("%-6d", e.Ppid))
			case "ret":
				sb.WriteString(fmt.Sprintf("%-3d", e.Retval))
			case "args":
				for _, arg := range e.Args {
					sb.WriteString(fmt.Sprintf("%s ", arg))
				}
			}
			sb.WriteRune(' ')
		}
	}

	return sb.String()
}

func getCustomExecsnoopColsHeader(cols []string) string {
	var sb strings.Builder

	for _, col := range cols {
		switch col {
		case "node":
			sb.WriteString(fmt.Sprintf("%-16s", "NODE"))
		case "namespace":
			sb.WriteString(fmt.Sprintf("%-16s", "NAMESPACE"))
		case "pod":
			sb.WriteString(fmt.Sprintf("%-16s", "POD"))
		case "container":
			sb.WriteString(fmt.Sprintf("%-16s", "CONTAINER"))
		case "pcomm":
			sb.WriteString(fmt.Sprintf("%-16s", "PCOMM"))
		case "pid":
			sb.WriteString(fmt.Sprintf("%-6s", "PID"))
		case "ppid":
			sb.WriteString(fmt.Sprintf("%-6s", "PPID"))
		case "ret":
			sb.WriteString(fmt.Sprintf("%-3s", "RET"))
		case "args":
			sb.WriteString(fmt.Sprintf("%-24s", "ARGS"))
		}
		sb.WriteRune(' ')
	}

	return sb.String()
}