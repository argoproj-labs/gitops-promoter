/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package version

import (
	"fmt"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Get", func() {
	Context("local builds", func() {
		It("returns default version metadata", func() {
			v := Get()
			Expect(v.Version).To(Equal("dev"))
			Expect(v.BuildDate).To(Equal("unknown"))
			Expect(v.GoVersion).To(Equal(runtime.Version()))
			Expect(v.Compiler).To(Equal(runtime.Compiler))
			Expect(v.Platform).To(Equal(fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)))
		})
	})
})
