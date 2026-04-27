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

package template

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/argoproj-labs/gitops-promoter/internal/webrequest/cli"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newWebRequestCommand() *cobra.Command {
	var bundlePath string
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "webrequest",
		Short: "Simulate one WebRequestCommitStatus reconcile from a YAML bundle",
		Long: `Reads a multi-document YAML bundle (WebRequestCommitStatus, PromotionStrategy,
and optional SimulatorConfig), runs webrequestsimulator.Simulate, and prints the result.

Output fields mirror a single controller reconcile: status (updated WebRequestCommitStatus.Status),
renderedRequests (rendered HTTP snapshots when a request runs), and commitStatuses (CommitStatus
CRs that would be upserted).

The bundle uses "---" separators between documents. Kinds must be WebRequestCommitStatus,
PromotionStrategy, and optionally SimulatorConfig (HTTP mocks and namespace metadata).`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Root `promoter` enables zap/klog; discard here so machine-readable output stays clean on stdout.
			ctrl.SetLogger(logr.Discard())
			klog.SetLogger(logr.Discard())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			raw, err := os.ReadFile(bundlePath)
			if err != nil {
				return fmt.Errorf("read bundle: %w", err)
			}
			in, err := cli.LoadBundle(raw)
			if err != nil {
				return err
			}
			res, err := webrequestsimulator.Simulate(ctx, *in)
			if err != nil {
				return err
			}
			var out []byte
			switch strings.ToLower(outputFormat) {
			case "json":
				out, err = cli.EncodeResultJSON(res)
			case "yaml", "yml":
				out, err = cli.EncodeResultYAML(res)
			default:
				return fmt.Errorf("unsupported --output %q (use json or yaml)", outputFormat)
			}
			if err != nil {
				return err
			}
			if _, err := cmd.OutOrStdout().Write(out); err != nil {
				return err
			}
			if len(out) > 0 && out[len(out)-1] != '\n' {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&bundlePath, "filename", "f", "", "path to multi-document YAML bundle")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "json", "output format: json or yaml")
	_ = cmd.MarkFlagRequired("filename")
	_ = cmd.MarkFlagFilename("filename", "yaml", "yml")
	return cmd
}
