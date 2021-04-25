//
// -*- coding: utf-8 -*-
//
// April 24 2021, Christian Hopps <chopps@gmail.com>
//
// Copyright (c) 2021, Christian Hopps
// All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

var userInfo *user.User

func expandTilde(path string) string {
	var err error
	if userInfo == nil {
		if userInfo, err = user.Current(); err != nil {
			log.Fatalf("expandTilde: %v", err)
		}
	}

	if i := strings.Index(path, "/~/"); i != -1 {
		return strings.Join([]string{userInfo.HomeDir, path[i+3:]}, "/")
	} else if ok := strings.HasPrefix(path, "~/"); ok {
		return strings.Join([]string{userInfo.HomeDir, path[2:]}, "/")
	} else if ok := strings.HasSuffix(path, "/~"); ok {
		return userInfo.HomeDir
	} else if path == "~" {
		return userInfo.HomeDir
	}
	return path
}

func getValue(line, keyword string) (bool, string) {
	l := strings.TrimSpace(line)
	if !strings.HasPrefix(l, keyword) {
		return false, ""
	}
	l = l[len(keyword):]
	// Check for keyword only
	if len(l) == 0 {
		return true, ""
	}
	// Make sure this isn't just a prefix of a keyword
	if l[0] != '\t' && l[0] != ' ' {
		return false, ""
	}
	l = l[1:]

	// Strip quotes around a single value
	r := csv.NewReader(strings.NewReader(l))
	r.Comma = ' '
	record, err := r.Read()
	if err != nil {
		log.Fatalf("getValue: %v", err)
	}
	if len(record) == 1 {
		return true, record[0]
	}
	return true, strings.TrimSpace(l)
}

// func getValues(line, keyword string) (bool, []string) {
// 	ok, l := getValue(line, keyword)
// 	if !ok {
// 		return false, []string{}
// 	}

// 	r := csv.NewReader(strings.NewReader(l))
// 	r.Comma = ' '

// 	record, err := r.Read()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	return true, record
// }

type AccountConfig struct {
	Name       string
	Host       string
	Port       int
	StartTLS   bool
	SSLVersion string
	User       string
	PassCmd    string
	password   string
}

type Channel struct {
	Name string
	Far  string
}

type IMAPStore struct {
	Name     string
	Account  string
	Config   AccountConfig
	Channels []*Channel // config ordered channel list
}

func finishAccountConfig(name string, config *AccountConfig) error {
	if config.Host == "" {
		return fmt.Errorf("Host required for %v", name)
	}
	if config.User == "" {
		return fmt.Errorf("User required for %v", name)
	}
	if config.password == "" && config.PassCmd == "" {
		return fmt.Errorf("Password or PassCmd required for %v", name)
	}
	if config.Port == 0 {
		config.Port = 993
		if config.SSLVersion == "" {
			config.SSLVersion = "TLSv1.2"
		}
	}
	return nil
}

func parseFile(fileName string) (map[string]*IMAPStore, error) {
	var accounts = make(map[string]*AccountConfig)
	var stores = make(map[string]*IMAPStore)
	var channels = make(map[string]*Channel)
	var chlist []*Channel

	f, err := os.Open(expandTilde(fileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	finishSection := func(a *AccountConfig, st *IMAPStore, ch *Channel) error {
		if a != nil {
			if err := finishAccountConfig(a.Name, a); err != nil {
				return err
			}
			accounts[a.Name] = a
		} else if st != nil {
			if st.Account == "" {
				if err := finishAccountConfig(st.Name, &st.Config); err != nil {
					return err
				}
			}
			stores[st.Name] = st
		} else if ch != nil {
			if ch.Far == "" {
				return fmt.Errorf("No Far given for Channel %v", ch.Name)
			}
			channels[ch.Name] = ch
			chlist = append(chlist, ch)
		}
		return nil
	}

	// Parse the file creating accounts
	var a *AccountConfig = nil
	var st *IMAPStore = nil
	var ch *Channel = nil
	otherSection := false
	scanner := bufio.NewScanner(f)
	lineno := 0
	for scanner.Scan() {
		lineno += 1

		l := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(l, "#") {
			continue
		}

		// Blank lines terminate a section
		if l == "" {
			if err := finishSection(a, st, ch); err != nil {
				return nil, err
			}
			a = nil
			st = nil
			ch = nil
			otherSection = false
			continue
		}

		// Not in any section, only look for section starts
		if a == nil && st == nil && ch == nil && !otherSection {
			if ok, v := getValue(l, "IMAPAccount"); ok {
				if _, ok := accounts[v]; ok {
					return nil, fmt.Errorf("Duplicate Account %v", v)
				}
				log.Debugf("Adding account %v", v)
				a = &AccountConfig{
					Name: v,
				}
			} else if ok, v := getValue(l, "IMAPStore"); ok {
				if _, ok := stores[v]; ok {
					return nil, fmt.Errorf("Duplicate IMAPStore %v", v)
				}
				log.Debugf("Adding IMAPStore %v", v)
				st = &IMAPStore{
					Name: v,
				}
			} else if ok, v := getValue(l, "Channel"); ok {
				if _, ok := channels[v]; ok {
					return nil, fmt.Errorf("Duplicate Channel %v", v)
				}
				log.Debugf("Adding Channel %v", v)
				ch = &Channel{
					Name: v,
				}
			} else {
				log.Debugf("%d: Skipping past section: \"%v\"", lineno, l)
				otherSection = true
			}
			continue
		}

		if ch != nil {
			if ok, v := getValue(l, "Far"); ok {
				if ch.Far != "" {
					return nil, fmt.Errorf("Multiple Far specified for channel %v", ch.Name)
				}
				ch.Far = v
			}
			continue
		} else if a != nil || st != nil {
			if st != nil {
				a = &st.Config
			}
			// First handle the account values that might occur in
			// the IMAPStore a well
			if ok, v := getValue(l, "Host"); ok {
				a.Host = v
			} else if ok, v := getValue(l, "PassCmd"); ok {
				a.PassCmd = v
			} else if ok, v := getValue(l, "Password"); ok {
				a.password = v
			} else if ok, v := getValue(l, "Port"); ok {
				if a.Port, err = strconv.Atoi(v); err != nil {
					return nil, err
				}
			} else if ok, v := getValue(l, "SSLType"); ok {
				if v == "STARTTLS" {
					a.StartTLS = true
					if a.Port == 0 {
						a.Port = 143
					}
				} else if v == "IMAPS" {
					if a.Port == 0 {
						a.Port = 993
					}
				} else {
					return nil, fmt.Errorf("Unknown SSLType %s", v)
				}
				if a.SSLVersion == "" {
					a.SSLVersion = "TLSv1.2"
				}
			} else if ok, v := getValue(l, "SSLVersion"); ok {
				a.SSLVersion = v
				if v != "None" {
					if a.Port == 0 {
						a.Port = 993
					}
				}
			} else if ok, v := getValue(l, "User"); ok {
				a.User = v
			}

			// If we are processing an actual account we are done
			if st == nil {
				continue
			}

			// Forget the store account config
			a = nil
			if ok, v := getValue(l, "Account"); ok {
				st.Account = v
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// EOF also finishes the section
	if err := finishSection(a, st, ch); err != nil {
		return nil, err
	}

	// Now go back over all stores and copy any account config if referenced
	for k, v := range stores {
		if v.Account != "" {
			if a, ok := accounts[v.Account]; !ok {
				return nil, fmt.Errorf("Store %v specifies non-existent account %v", k, v.Account)
			} else {
				v.Config = *a
			}
		}
	}

	// Create config ordered list of channels per store
	for i := range chlist {
		ch := chlist[i]
		far := ch.Far[1 : len(ch.Far)-1]
		st, ok := stores[far]
		if !ok {
			return nil, fmt.Errorf("Channel specifies non-existent Far store %v", far)
		}
		st.Channels = append(st.Channels, ch)
	}

	return stores, nil
}
