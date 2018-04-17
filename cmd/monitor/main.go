/*
Copyright (c) 2018 Red Hat, Inc.

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
	"encoding/json"
	"flag"
	"os"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/monitor-project-lifecycle/client"
)

func main() {
	rootCmd := &cobra.Command{
		Use:  "monitor",
		Long: "The app lifecycle monitor constantly measures the availability of application creation operations.",
	}

	rootCmd.AddCommand(NewRunCommand())
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	// This is needed to make `glog` believe that the flags have already been parsed, otherwise
	// every log messages is prefixed by an error message stating the the flags haven't been
	// parsed.
	flag.CommandLine.Parse([]string{})
	defer glog.Flush()

	// Execute the root command:
	rootCmd.SetArgs(os.Args[1:])
	rootCmd.Execute()
}

// RunOptions represent command line flags.
type RunOptions struct {
	ConfigFile string
}

// NewRunCommand makes a command for running the test.
func NewRunCommand() *cobra.Command {
	options := &RunOptions{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Starts the monitor app.",
		Long:  "Starts the monitor app.",
		Run: func(c *cobra.Command, args []string) {
			config, err := readConfig(options.ConfigFile)
			if err != nil {
				glog.Fatalf("Fatal error: %v\n", err)
			}
			// before we bring everything up, we need to make sure that we have
			// a working configuration to connect to the API server
			restconfig, err := client.GetRestConfig()
			if err != nil {
				glog.Fatalf("couldn't create restconfig: %v", err)
			}
			clients, err := client.MakeRESTClients(restconfig)
			if err != nil {
				glog.Fatalf("couldn't make rest client: %v", err)
			}
			configStr, _ := json.Marshal(config)
			glog.Infof("starting monitor app with config: %s", configStr)
			monitor := &AppCreateAvailabilityMonitor{
				Client: clients,
				Config: config,
				Stop:   make(chan struct{}),
			}
			monitor.Run()
		},
	}

	flags := cmd.Flags()
	// This command only supports reading from config
	flags.StringVar(&options.ConfigFile, "config", "", "Location of the configuration file to run from.")
	cmd.MarkFlagFilename("config", "yaml", "yml")
	cmd.MarkFlagRequired("config")

	return cmd
}
