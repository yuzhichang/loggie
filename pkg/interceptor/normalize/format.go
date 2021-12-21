/*
Copyright 2021 Loggie Authors

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

package normalize

import (
	"strconv"

	"loggie.io/loggie/pkg/core/api"
	"loggie.io/loggie/pkg/core/log"
)

const ProcessorFormat = "format"

const (
	formatBoolean = "bool"
	formatInteger = "integer"
	formatFloat   = "float"
	formatDefault = "string"
)

type FormatProcessor struct {
	config *FormatConfig
}

type FormatConfig struct {
	Format    map[string]string `yaml:"format,omitempty"`
	UnderRoot bool              `yaml:"underRoot,omitempty" default:"true"`
}

func init() {
	register(ProcessorFormat, func() Processor {
		return newFormatProcessor()
	})
}

func newFormatProcessor() *FormatProcessor {
	return &FormatProcessor{
		config: &FormatConfig{},
	}
}

func (p *FormatProcessor) Config() interface{} {
	return p.config
}

func (p *FormatProcessor) Init() {
	log.Info("format: %v", p.config.Format)
}

func (p *FormatProcessor) Process(e api.Event) error {
	if p.config == nil {
		return nil
	}

	header := e.Header()
	if header == nil {
		return nil
	}

	var src map[string]interface{}
	if p.config.UnderRoot {
		src = header
	} else {
		paramsMap, exist := header[SystemLogBody]
		if !exist {
			return nil
		}
		val, ok := paramsMap.(map[string]interface{})
		if !ok {
			return nil
		}

		src = val
	}

	for k, v := range src {
		dstFormat, exist := p.config.Format[k]
		if exist {
			srcVal, ok := v.(string)
			if ok {
				src[k] = format(srcVal, dstFormat)
			}
		}
	}

	if p.config.UnderRoot {
		header = src
	} else {
		header[SystemLogBody] = src
	}

	return nil
}

func format(srcVal, dstFormat string) interface{} {
	switch dstFormat {
	case formatBoolean:
		dstVal, err := strconv.ParseBool(srcVal)
		if err != nil {
			goto original
		}
		return dstVal
	case formatInteger:
		dstVal, err := strconv.ParseInt(srcVal, 10, 64)
		if err != nil {
			goto original
		}
		return dstVal
	case formatFloat:
		dstVal, err := strconv.ParseFloat(srcVal, 64)
		if err != nil {
			goto original
		}
		return dstVal
	}

original:
	return srcVal
}
