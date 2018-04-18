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
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/ghodss/yaml"
)

// Config is the configuration for the test runner.
type Config struct {
	// ListenAddress is the bind addr for the metrics server.
	ListenAddress string `json:"listenAddress"`
	// RunInterval is how often to repeat the test.
	RunInterval Duration `json:"runInterval"`
	// AvailabilityTimeout is how long to wait for the app route before giving up.
	AvailabilityTimeout Duration `json:"availabilityTimeout"`
	// Template configures a template to instantiate for the test.
	Template struct {
		// Name is the name of the template.
		Name string `json:"name"`
		// Namespace is where the template lives.
		Namespace string `json:"namespace"`
		// Parameters are passed to the template.
		Parameters map[string]string `json:"parameters"`
		// AvailabilityRoute is the name of a route produced by the template to
		// assess availability.
		AvailabilityRoute string `json:"availabilityRoute"`
	} `json:"template"`
}

func readConfig(filename string) (*Config, error) {
	// Ensure the entire path is there for reading.
	absFilename, _ := filepath.Abs(filename)

	// Actually read in the file as a byte array
	yamlFile, err := ioutil.ReadFile(absFilename)
	if err != nil {
		return nil, err
	}

	// Convert the yaml byte array into the config structure object.
	var cfg Config
	err = yaml.Unmarshal(yamlFile, &cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Duration is a time.Duration that uses duration strings as the serialization
// format.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	tmp, err := time.ParseDuration(s)
	if err != nil {
		return err
	}

	d.Duration = tmp

	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}
