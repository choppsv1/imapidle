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
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	// Version : current version
	Version string = strings.TrimSpace(version)
	Sha     string = strings.TrimSpace(sha)
	//go:embed version.txt
	version string
	//go:embed .build-sha.txt
	sha string
)

func dumpValue(i interface{}) {
	b, err := json.Marshal(i)
	if err != nil {
		log.Fatal(err)
	}
	log.Debugf("%s\n", string(b))
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func runUpdateScript(script string, updateNames []string) {
	log.Debugf("Running update script %s with args: %s", script, updateNames)

	sPath, err := exec.LookPath(expandTilde(script))
	if err != nil {
		log.Errorf("Cannot find update script %s in PATH", sPath)
		return
	}
	log.Debugf("Update script found: %s", sPath)

	args := make([]string, len(updateNames)+1)
	// args = append(args, sPath)
	for i := range updateNames {
		args = append(args, updateNames[i])
	}

	cmd := &exec.Cmd{
		Path:   sPath,
		Args:   args,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	if err = cmd.Run(); err != nil {
		log.Warnf("%s: returned an error: %v", script, err)
	}
}

func main() {
	var updateScript, mbsyncrc string
	var interval time.Duration

	flag.StringVar(&updateScript, "update-script", "~/.imapidle-update", "Script to run when an INBOX is updated")
	flag.StringVar(&mbsyncrc, "mbsyncrc", "~/.mbsyncrc", "Location of mbsync config file")
	flag.DurationVar(&interval, "full-interval", DefPollInterval, "Time between full updates regardless of IDLE")
	versionFlag := flag.Bool("version", false, "Print the version and exit")
	verboseFlag := flag.Bool("verbose", false, "Log verbosely")
	debugFlag := flag.Bool("debug", false, "Log information useful for debugging")
	flag.NArg()
	flag.Parse()
	checkStores := flag.Args()

	if *versionFlag {
		fmt.Printf("Version %s (%s)\n", strings.Split(Version, "\n")[0], Sha)
		os.Exit(0)
	}

	if *verboseFlag {
		log.SetLevel(log.DebugLevel)
	}
	if *debugFlag {
		log.SetLevel(log.TraceLevel)
	}
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02-2006 15:04:05.000",
	})

	stores, err := parseFile(mbsyncrc)
	if err != nil {
		log.Fatal("parseFile: ", err)
	}

	var accounts = make(map[string]*Account)
	for k, v := range stores {
		if len(v.Channels) == 0 {
			log.Infof("Skipping store %v due to no channels", k)
			continue
		}

		a := &Account{
			AccountConfig: v.Config,
			Channels:      v.Channels,
			PollInt:       interval,
		}

		// Fix the name to be the same as the store
		a.Name = k

		// Check for user restrictions
		if len(checkStores) != 0 {
			i := 0
			for i = range checkStores {
				vals := strings.Split(checkStores[i], ":")
				vlen := len(vals)
				sname := vals[0]
				if sname != k {
					continue
				}
				if vlen == 3 {
					// User specified store channel and inbox name
					a.UpdateName = strings.Join(vals[1:], ":")
				} else if vlen == 2 {
					a.UpdateName = fmt.Sprintf("%s:INBOX", vals[1])
				} else if vlen == 1 {
					a.UpdateName = fmt.Sprintf("%s:INBOX", a.Channels[0].Name)
				} else {
					log.Errorf("Bad store/channel name %v", checkStores[i])
					flag.Usage()
					os.Exit(1)
				}
				break
			}
			if i == len(checkStores) {
				// Skip this store as not specified by user
				continue
			}
		} else {
			// Set the update name
			a.UpdateName = fmt.Sprintf("%s:INBOX", a.Channels[0].Name)
		}

		accounts[k] = a
	}

	dumpValue(accounts)

	// Channel to receive account events
	eventc := make(chan Event, 1)

	for _, a := range accounts {
		go a.Online(eventc)
	}

	// Periodically do a full update
	go func() {
		ft := time.NewTimer(interval)
		for {
			eventc <- Event{FullUpdateEvent, nil}
			<-ft.C
			ft.Reset(interval)
		}
	}()

	update := make(map[string]bool)
	fullUpdate := false
	dampT := time.NewTimer(10 * time.Minute)
	dampT.Stop() // Stop immediately
	log.Debugf("Damped timer created and stopped")

	defer log.Error("Exited Main!")

	for {
		log.Debugf("Main select")
		select {
		case e := <-eventc:
			switch e.E {
			case NewMailEvent:
				log.Debugf("Received NewMailEvent: %v", e.A.Name)
				if !fullUpdate {
					// Timer hasn't been set yet -- set.
					if len(update) == 0 {
						// Wait 1 second for other accounts

						log.Debugf("[Re]Setting damp timer")
						dampT.Reset(time.Second)
					}
					update[e.A.Name] = true
				}
			case FullUpdateEvent:
				log.Debugf("Received FullUpdateEvent")
				if !fullUpdate {
					if len(update) != 0 {
						update = make(map[string]bool)
					} else {
						// Timer hasn't been set yet -- set.
						log.Debugf("[Re]Setting damp timer")
						dampT.Reset(time.Second)
					}
				}
				fullUpdate = true
			}
		case <-dampT.C:
			log.Debugf("Damped timer fires (stopped)")
			if fullUpdate {
				fullUpdate = false
			}
			channels := make([]string, 0, len(update))
			for k := range update {
				channels = append(channels, accounts[k].UpdateName)
			}
			// Clear update tracker
			update = make(map[string]bool)
			runUpdateScript(updateScript, channels)
		}
	}
	log.Panic("NOTREACHED")
}
