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

package types

import (
	"github.com/lato333/inspektor-gadget/pkg/columns"
	eventtypes "github.com/lato333/inspektor-gadget/pkg/types"
)

const (
	ProfileUserParam   = "user"
	ProfileKernelParam = "kernel"
)

type Report struct {
	eventtypes.CommonData

	Comm        string   `json:"comm,omitempty" column:"comm,template:comm"`
	Pid         uint32   `json:"pid,omitempty" column:"pid,template:pid"`
	UserStack   []string `json:"userStack,omitempty"`
	KernelStack []string `json:"kernelStack,omitempty"`
	Count       uint64   `json:"count,omitempty" column:"count"`
}

func GetColumns() *columns.Columns[Report] {
	return columns.MustCreateColumns[Report]()
}
