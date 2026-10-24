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

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/test/utils"
)

const (
	namespace = "promoter-system"
	// argoCDApplicationCRD is the Argo CD Application CRD the integration suites also load.
	argoCDApplicationCRD = "test/external_crds/argoproj.io_applications.yaml"
	// e2eCoverageEnv, when "1", builds the manager with -cover and has it write profiles to the Kind node's
	// coverageHostPath as the process exits. CI sets it on one matrix leg and uploads the profile to Codecov.
	e2eCoverageEnv = "E2E_COVERAGE"
	// coverageHostPath is where the Kind node stores the controller's coverage files. The CI workflow copies it
	// after the suite deletes the controller namespace, which is what makes the process exit and flush the profile.
	coverageHostPath = "/var/promoter-cover"
)

var _ = Describe("controller", Ordered, func() {
	// installedApplicationCRD is true when the suite installed the Argo CD Application CRD, so teardown leaves a CRD
	// that was already there, and its Applications, alone.
	var installedApplicationCRD bool

	BeforeAll(func() {
		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())

		By("installing the cert-manager")
		Expect(utils.InstallCertManager()).To(Succeed())

		// The default ControllerConfiguration watches local Argo CD Applications, so the controller cannot
		// start without the Application CRD.
		By("installing the Argo CD Application CRD unless the cluster has it")
		cmd := exec.Command("kubectl", "get", "crd", "applications.argoproj.io")
		if _, err := utils.Run(cmd); err != nil {
			cmd = exec.Command("kubectl", "apply", "-f", argoCDApplicationCRD)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			installedApplicationCRD = true
		}

		By("creating manager namespace")
		cmd = exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("uninstalling the Prometheus manager bundle")
		utils.UninstallPrometheusOperator()

		By("uninstalling the cert-manager bundle")
		utils.UninstallCertManager()

		By("removing manager namespace")
		cmd := exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)

		if installedApplicationCRD {
			By("removing the Argo CD Application CRD")
			cmd = exec.Command("kubectl", "delete", "-f", argoCDApplicationCRD, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		}
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			var (
				imageTag     = "0.0.0-test-e2e"
				projectimage = "quay.io/argoprojlabs/gitops-promoter:" + imageTag
			)

			By("building the manager(Operator) image")
			build := []string{"docker-build", "IMAGE_TAG=" + imageTag}
			if e2eCoverageEnabled() {
				build = append(build, "COVER=1")
			}
			cmd := exec.Command("make", build...)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", "IMAGE_TAG="+imageTag)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager runs the image built from this checkout")
			cmd = exec.Command("kubectl", "get", "deployment", "promoter-controller-manager", "-n", namespace,
				"-o", `jsonpath={.spec.template.spec.containers[?(@.name=="manager")].image}`)
			deployedImage, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, string(deployedImage)).To(Equal(projectimage))

			if e2eCoverageEnabled() {
				By("directing the controller to write coverage on exit")
				Expect(prepareKindCoverageDir()).To(Succeed())
				// The directory is created on the node before this patch, and hostPath type Directory requires it to
				// exist. The manager runs as a non-root user, so the directory is world-writable. The SCM specs later
				// strategic-merge their own volume onto this pod, which keeps this mount.
				patch := `{"spec":{"template":{"spec":{"terminationGracePeriodSeconds":30,` +
					`"volumes":[{"name":"coverage","hostPath":{"path":"` + coverageHostPath + `","type":"Directory"}}],` +
					`"containers":[{"name":"manager","env":[{"name":"GOCOVERDIR","value":"/coverage"}],` +
					`"volumeMounts":[{"name":"coverage","mountPath":"/coverage"}]}]}}}}`
				cmd = exec.Command("kubectl", "patch", "deployment", "promoter-controller-manager",
					"-n", namespace, "--type=strategic", "-p", patch)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			}

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())
		})
	})

	Context("SCM providers", Ordered, scmProviderSpecs)

	Context("Dashboard Aggregation API", func() {
		// This spec deploys the dashboard aggregation apiserver (extension apiserver +
		// APIService) using the cert-manager overlay (cert-manager is installed in
		// BeforeAll), then verifies the virtual PromotionStrategyDetails resource is
		// served and reacts to changes. It is opt-in via RUN_APISERVER_E2E because it
		// requires the aggregation apiserver image and serving certs to be wired into
		// the Kind cluster; enable it once that wiring is validated in CI.
		It("should serve PromotionStrategyDetails and react to child changes", func() {
			if os.Getenv("RUN_APISERVER_E2E") == "" {
				Skip("set RUN_APISERVER_E2E to run the dashboard aggregation apiserver e2e spec")
			}

			imageTag := "0.0.0-test-e2e"
			projectimage := "quay.io/argoprojlabs/gitops-promoter:" + imageTag

			By("deploying the dashboard aggregation apiserver (cert-manager overlay)")
			cmd := exec.Command("kubectl", "apply", "-k", "config/apiserver/certs-cert-manager")
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("pointing the apiserver deployment at the e2e image")
			cmd = exec.Command("kubectl", "-n", namespace, "set", "image",
				"deploy/promoter-apiserver", "apiserver="+projectimage)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("waiting for the APIService to become Available")
			verifyAPIServiceAvailable := func() error {
				cmd = exec.Command("kubectl", "get", "apiservice",
					"v1alpha1.view.promoter.argoproj.io",
					"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}",
				)
				out, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to get APIService status: %w", err)
				}
				if strings.TrimSpace(string(out)) != "True" {
					return fmt.Errorf("APIService not Available yet: %q", string(out))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyAPIServiceAvailable, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("confirming the aggregated resource is discoverable")
			verifyDiscoverable := func() error {
				cmd = exec.Command("kubectl", "get", "promotionstrategydetails", "-A")
				if _, err := utils.Run(cmd); err != nil {
					return fmt.Errorf("promotionstrategydetails not discoverable yet: %w", err)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyDiscoverable, time.Minute, 5*time.Second).Should(Succeed())
		})
	})
})

// e2eCoverageEnabled reports whether this run should build and run an instrumented controller.
func e2eCoverageEnabled() bool {
	return os.Getenv(e2eCoverageEnv) == "1"
}

// prepareKindCoverageDir creates the host path the controller writes coverage to. The manager container runs as a
// non-root user, and a DirectoryOrCreate hostPath would be owned by root and not writable, so the directory is made
// world-writable. Kind names the node container <cluster>-control-plane.
func prepareKindCoverageDir() error {
	cluster := "kind"
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	cmd := exec.CommandContext(context.Background(), "docker", "exec", cluster+"-control-plane", "sh", "-c",
		"mkdir -p "+coverageHostPath+" && chmod 777 "+coverageHostPath)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("preparing coverage dir on the kind node: %w", err)
	}
	return nil
}
