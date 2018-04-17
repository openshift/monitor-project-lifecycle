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
	ListenAddress string `json:"listenAddress"`
	Check         struct {
		Namespace   string `json:"namespace"`
		DisplayName string `json:"displayName"`
		Route       string `json:"route"`
	} `json:"check"`
	RunInterval Duration `json:"runInterval"`
	Timeout     struct {
		TemplateCreation Duration `json:"templateCreation"`
		TemplateDeletion Duration `json:"templateDeletion"`
	} `json:"timeout"`
	Template struct {
		Name       string            `json:"name"`
		Namespace  string            `json:"namespace"`
		Parameters map[string]string `json:"parameters"`
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
