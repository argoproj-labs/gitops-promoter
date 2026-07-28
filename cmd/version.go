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

package main

import (
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/common"
	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	var short bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			v := common.GetVersion()
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), v.Version)
				return
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", common.CommandCLI, v.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "  BuildDate: %s\n", v.BuildDate)
			fmt.Fprintf(cmd.OutOrStdout(), "  GoVersion: %s\n", v.GoVersion)
			fmt.Fprintf(cmd.OutOrStdout(), "  Compiler: %s\n", v.Compiler)
			fmt.Fprintf(cmd.OutOrStdout(), "  Platform: %s\n", v.Platform)
		},
	}

	cmd.Flags().BoolVar(&short, "short", false, "Print just the version number")
	return cmd
}
