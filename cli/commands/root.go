// Copyright (c) 2016 Pani Networks
// All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"github.com/romana/core/common"

	log "github.com/romana/rlog"
	cli "github.com/spf13/cobra"
	config "github.com/spf13/viper"
)

// Variables used for configuration and flags.
var (
	cfgFile    string
	rootURL    string
	rootPort   string
	version    bool
	verbose    bool
	format     string
	platform   string
	credential *common.Credential
)

type Error struct {
	Code    int32
	Message string
	Fields  string
}

func (e Error) Error() string {
	return fmt.Sprintf("%d: %v: %v", e.Code, e.Message, e.Fields)
}

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cli.Command{
	Use:   "romana",
	Short: "Command line tools for romana services.",
	Long: `Command line tools for romana services.

For more information, please check http://romana.io
`,
}

// Execute adds all child commands to the root command and sets
// flags appropriately. This is called by main.main(). It only
// needs to happen once to the rootCmd. Here commands/subcommand
// mapping is added where control is passed around from main()
// to commands/subcommands evoked.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(-1)
	}
}

func init() {
	credential = common.NewCredentialCobra(RootCmd)

	cli.OnInitialize(initConfig)
	RootCmd.AddCommand(hostCmd)
	RootCmd.AddCommand(tenantCmd)
	RootCmd.AddCommand(segmentCmd)
	RootCmd.AddCommand(policyCmd)

	RootCmd.Flags().BoolVarP(&version, "version", "",
		false, "Build and Versioning Information.")

	RootCmd.PersistentFlags().StringVarP(&cfgFile, "config",
		"c", "", "config file (default is $HOME/.romana.yaml)")
	RootCmd.PersistentFlags().StringVarP(&rootURL, "rootURL",
		"r", "", "root service url, e.g. http://192.168.0.1")
	RootCmd.PersistentFlags().StringVarP(&rootPort, "rootPort",
		"p", "", "root service port, e.g. 9600")
	RootCmd.PersistentFlags().StringVarP(&format, "format",
		"f", "", "enable formatting options like [json|table], etc.")
	RootCmd.PersistentFlags().StringVarP(&platform, "platform",
		"P", "", "Use platforms like [openstack|kubernetes], etc.")
	RootCmd.PersistentFlags().BoolVarP(&verbose, "verbose",
		"v", false, "Verbose output.")

	RootCmd.PersistentPreRun = preConfig
	RootCmd.Run = versionInfo
}

// preConfig sanitizes URLs and sets up config with URLs.
func preConfig(cmd *cli.Command, args []string) {
	var baseURL string

	// Add port details to rootURL else try localhost
	// if nothing is given on command line or config.
	if rootURL == "" {
		rootURL = config.GetString("RootURL")
	}
	if rootPort == "" {
		rootPort = config.GetString("RootPort")
	}
	if rootPort == "" {
		re, _ := regexp.Compile(`:\d+/?`)
		port := re.FindString(rootURL)
		port = strings.TrimPrefix(port, ":")
		port = strings.TrimSuffix(port, "/")
		if port != "" {
			rootPort = port
		} else {
			rootPort = "9600"
		}
	}
	config.Set("RootPort", rootPort)
	if rootURL != "" {
		baseURL = strings.TrimSuffix(rootURL, "/")
		baseURL = strings.TrimSuffix(baseURL, ":9600")
		baseURL = strings.TrimSuffix(baseURL, ":"+rootPort)
	} else {
		baseURL = "http://localhost"
	}
	config.Set("BaseURL", baseURL)
	rootURL = baseURL + ":" + rootPort + "/"
	config.Set("RootURL", rootURL)

	// Give command line options higher priority then
	// the corresponding config options.
	if format == "" {
		format = config.GetString("Format")
	}
	// if format is still not found just default to tabular format.
	if format == "" {
		format = "table"
	}
	config.Set("Format", format)

	if platform == "" {
		platform = config.GetString("Platform")
	}
	if platform == "" {
		platform = "openstack"
	}
	config.Set("Platform", platform)

	fmt.Println(config.GetString("username"))
	err := credential.Initialize()
	if err != nil {
		log.Printf("Error: %s", err)
		os.Exit(1)
	}
}

// versionInfo displays the build and versioning information.
func versionInfo(cmd *cli.Command, args []string) {
	if version {
		fmt.Println(common.BuildInfo())
		os.Exit(0)
	}
	cmd.Help()
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	// https://github.com/spf13/viper/commit/5619c0 changes the behaviour
	// of SetConfigFile and SetConfigName, thus SetConfigName should come
	// before SetConfigFile.
	config.SetConfigName(".romana") // name of config file (without extension)
	config.AddConfigPath("$HOME")   // adding home directory as first search path

	if cfgFile != "" { // enable ability to specify config file via flag
		config.SetConfigFile(cfgFile)
	}

	config.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	err := config.ReadInConfig()
	setLogOutput()
	if err != nil {
		log.Println("Error using config file:", config.ConfigFileUsed())
	} else {
		log.Println("Using config file:", config.ConfigFileUsed())
	}
}

// setLogOutput sets the log output to a file of /dev/null
// depending on the configuration set during initialization.
func setLogOutput() {
	logFile, err := os.OpenFile(config.GetString("LogFile"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		if verbose || config.GetBool("Verbose") {
			// If output is verbose send it to log file
			// stdout simultenously.
			config.Set("Verbose", true)
			log.SetOutput(io.MultiWriter(logFile, os.Stdout))
		} else {
			// Redirect log output to the log file.
			log.SetOutput(logFile)
		}
	} else {
		if verbose || config.GetBool("Verbose") {
			config.Set("Verbose", true)
			log.SetOutput(os.Stdout)
		} else {
			// Silently fail and discard log output
			log.SetOutput(ioutil.Discard)
		}
	}
}

// JSONFormat indents input json b and writes it to
// output writer w.
func JSONFormat(b []byte, w io.Writer) {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "\t"); err != nil {
		// reset out since it may have partial output from above.
		out.Reset()
		// indentation failed, so write the original string as is.
		out.Write(b)
		out.WriteTo(w)
		return
	}
	out.WriteTo(w)
}
