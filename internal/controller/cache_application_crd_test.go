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

package controller

import (
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
)

// These specs live in the controller package because they need a real API server: whether Argo CD
// Application is listed in the host cache ByObject map is decided by discovery against the cluster.
var _ = Describe("WithArgoCDApplicationIfInstalled", func() {
	It("lists Application unfiltered when the CRD is installed on the host cluster", func() {
		opts, err := promotercache.WithArgoCDApplicationIfInstalled(
			promotercache.OptionsForInstanceID(nil, "default"),
			cfg,
		)
		Expect(err).NotTo(HaveOccurred())
		byObj, ok := opts.ByObject[promotercache.UnpartitionedApplicationObject()]
		Expect(ok).To(BeTrue())
		Expect(byObj.Label.String()).To(Equal(labels.Everything().String()),
			"Applications never carry the instance-id label, so they must stay unfiltered")
	})

	It("omits Application and still builds a cache when the CRD is absent", func() {
		// The Argo CD Application CRD ships in test/external_crds, so loading the promoter CRDs
		// alone reproduces a cluster without Argo CD installed.
		env := &envtest.Environment{
			UseExistingCluster: new(false),
			CRDDirectoryPaths: []string{
				filepath.Join("..", "..", "config", "crd", "bases"),
			},
			ErrorIfCRDPathMissing:    true,
			ControlPlaneStopTimeout:  1 * time.Minute,
			AttachControlPlaneOutput: false,
			BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
				fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
		}
		envCfg, err := env.Start()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(env.Stop()).To(Succeed())
		})

		opts, err := promotercache.WithArgoCDApplicationIfInstalled(
			promotercache.OptionsForInstanceID(nil, "default"),
			envCfg,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(opts.ByObject).NotTo(HaveKey(promotercache.UnpartitionedApplicationObject()))
		opts.Scheme = scheme

		// cache.New resolves a RESTMapping for every ByObject key, so listing Application here
		// would make the local manager fail to start when the Argo CD CRD is absent.
		_, err = cache.New(envCfg, opts)
		Expect(err).NotTo(HaveOccurred(),
			"the local manager must start when the Argo CD Application CRD is absent")
	})
})
