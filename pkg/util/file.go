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

package util

import (
	"bufio"
	"bytes"
	"github.com/loggie-io/loggie/pkg/core/log"
	"github.com/mattn/go-zglob"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// LineCountTo calculates the number of lines to the offset
func LineCountTo(offset int64, fileName string) (int, error) {
	r, err := os.Open(fileName)
	defer r.Close()
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 64*1024)
	count := 0
	lineSep := []byte{'\n'}
	totalReadBytes := int64(0)

	for totalReadBytes < offset {
		c, err := r.Read(buf)
		gap := totalReadBytes + int64(c) - offset
		if gap > 0 {
			c = c - int(gap) + 1
		}
		count += bytes.Count(buf[:c], lineSep)

		if err != nil {
			if err == io.EOF {
				return count, nil
			}
			return count, err
		}
		totalReadBytes += int64(c)
	}
	return count, nil
}

// LineCount returns the number of file lines
// better
func LineCount(r io.Reader) (int, error) {
	buf := make([]byte, 64*1024)
	count := 0
	lineSep := []byte{'\n'}

	for {
		c, err := r.Read(buf)
		count += bytes.Count(buf[:c], lineSep)

		if err != nil {
			if err == io.EOF {
				return count, nil
			}
			return count, err
		}
	}
}

// LineCount1 returns the number of file lines
// deprecated
func LineCount1(r io.Reader) (int, error) {
	fileScanner := bufio.NewScanner(r)
	lineCount := 0
	for fileScanner.Scan() {
		lineCount++
	}
	return lineCount, nil
}

func WriteFileOrCreate(dir string, filename string, content []byte) error {
	f := filepath.Join(dir, filename)
	_, err := os.Stat(dir)
	if err != nil {
		if !os.IsExist(err) {
			err = os.MkdirAll(dir, os.ModePerm)
			if err != nil {
				log.Panic("mkdir %s error: %v", dir, err)
			}
		}
		return err
	}
	return ioutil.WriteFile(f, content, os.ModePerm)
}

func GlobWithRecursive(pattern string) (matches []string, err error) {
	if strings.Contains(pattern, "**") {
		// recursive lookup
		matches, err = zglob.Glob(pattern)
	} else {
		matches, err = filepath.Glob(pattern)
	}
	return matches, err
}

func MatchWithRecursive(pattern, name string) (matched bool, err error) {
	if strings.Contains(pattern, "**") {
		// recursive lookup
		matched, err = zglob.Match(pattern, name)
	} else {
		matched, err = filepath.Match(pattern, name)
	}
	return matched, err
}
