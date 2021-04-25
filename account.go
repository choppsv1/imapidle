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
	"crypto/tls"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/responses"
	log "github.com/sirupsen/logrus"
)

const (
	IdleTimeout     = time.Duration(29) * time.Minute
	DefPollInterval = time.Duration(5) * time.Minute
)

type EventCode int

const (
	OfflineEvent = iota // Offline reaping the account is safe.
	NewMailEvent
	FullUpdateEvent
)

type Event struct {
	E EventCode
	A *Account
}

// An IDLE command.
// Se RFC 2177 section 3.
type Command struct{}

func (cmd *Command) Command() *imap.Command {
	return &imap.Command{
		Name: "IDLE",
	}
}

func (cmd *Command) Parse(fields []interface{}) error {
	log.Tracef("Command parse fields: %v", fields)
	return nil
}

// An IDLE response.
type Response struct {
	RepliesCh chan []byte
	Stop      <-chan struct{}

	gotContinuationReq bool
}

func (r *Response) Replies() <-chan []byte {
	log.Tracef("Response: returning Replies channel")
	return r.RepliesCh
}

func (r *Response) Handle(resp imap.Resp) error {
	log.Tracef("Response: Handle called with resp: %v", resp)

	// Wait for a continuation request, setup go routine to clean things up
	if cResp, ok := resp.(*imap.ContinuationReq); ok && !r.gotContinuationReq {
		log.Tracef("Response: Handle: ContinuationReq Info: %v", cResp.Info)
		r.gotContinuationReq = true

		// We got a continuation request, wait for r.Stop to be closed
		go func() {
			log.Tracef("Response: go-func waiting on r.Stop")
			<-r.Stop
			log.Tracef("Response: got r.Stop, calling r.stop()")
			r.stop()
		}()

		return nil
	}
	if dResp, ok := resp.(*imap.DataResp); ok {
		log.Tracef("Response: Handle: DataResp Tag: %s Fields: '%v'", dResp.Tag, dResp.Fields)
	}

	log.Tracef("Response: Handle: Unhandled")

	return responses.ErrUnhandled
}

func (r *Response) stop() {
	log.Debug("Response: stop called, sending DONE")
	r.RepliesCh <- []byte("DONE\r\n")
}

type Account struct {
	// Configuration
	AccountConfig
	Channels []*Channel

	UpdateName string // Channel:INBOX name to update for this acct
	PollInt    time.Duration

	// State
	MsgCount int // number of messages in INBOX

	c       *client.Client
	donec   chan error         // IDLE Command done notification
	eventc  chan<- Event       // receive events from the account
	stopc   chan struct{}      // signal IDLE command to exit
	updatec chan client.Update // Updates from IDLE command
	t       *time.Timer        // Timer for polling or IDLE refresh

	idleOk bool
}

func (a *Account) String() string {
	return fmt.Sprintf("ACCT: %s", a.Host)
}

func getPass(cmdstr string) (string, error) {
	// Get the password
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		return "", err
	}
	cmd := &exec.Cmd{
		Path: bashPath,
		Args: []string{bashPath, "-c", cmdstr},
	}
	var o []byte
	if o, err = cmd.Output(); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(o)), nil
}

func (a *Account) Login() error {
	var err error
	if a.password == "" {
		if a.password, err = getPass(a.PassCmd); err != nil {
			return err
		}
	}

	if a.c == nil {
		// Connect to server
		tlsConfig := &tls.Config{
			ServerName: a.Host,
			NextProtos: []string{a.SSLVersion},
		}
		if !a.StartTLS {
			if a.c, err = client.DialTLS(fmt.Sprintf("%s:%d", a.Host, a.Port), tlsConfig); err != nil {
				return err
			}
			log.Debugf("%v: Connected with TLS", a.Name)
		} else {
			if a.c, err = client.Dial(fmt.Sprintf("%s:%d", a.Host, a.Port)); err != nil {
				return err
			}
			log.Debugf("%v: Connected non-TLS", a.Name)

			// Start a TLS session
			if err := a.c.StartTLS(tlsConfig); err != nil {
				return err
			}
			log.Debugf("%v: TLS started", a.Name)
		}
	}

	if err := a.c.Login(a.User, a.password); err != nil {
		log.Warnf("%v: login %v failed", a.Name, a.User)
		return err
	}
	log.Debugf("%v: %s logged in", a.Name, a.User)

	if a.idleOk, err = a.c.Support("IDLE"); err != nil {
		a.idleOk = false
		return err
	}

	log.Debugf("%v: Support IDLE: %v", a.Name, a.idleOk)

	return nil
}

func (a *Account) Logout() {

	if a.c != nil {
		if a.stopc != nil {
			a.StopIdle(false)
		}
		a.c.Logout()
		a.c = nil
	}
}

func (a *Account) selectInbox() (mbox *imap.MailboxStatus, err error) {
	log.Debugf("%v: selecting INBOX", a.Name)

	mbox, err = a.c.Select("INBOX", false)
	if err != nil {
		return
	}
	a.MsgCount = int(mbox.Messages)
	log.Debugf("%v: %d Messages", a.Name, a.MsgCount)
	return
}

func (a *Account) PollPause() {
	timeout := a.PollInt
	if a.c == nil {
		log.Debugf("%v: pausing %ds for reconnect", a.Name, timeout/time.Second)
	} else if a.idleOk {
		log.Panicf("%v: Poll called when IDLE supported", a.Name)
	} else {
		log.Debugf("%v: pausing %ds for next poll", a.Name, timeout/time.Second)
	}
	t := time.NewTimer(timeout)
	<-t.C
}

func (a *Account) checkForNew() (int, error) {

	old := a.MsgCount
	if _, err := a.selectInbox(); err != nil {
		return 0, err
	}

	newCount := 0
	if old != 0 {
		newCount = a.MsgCount - old
	}
	return newCount, nil

}

func (a *Account) CheckForNew() {
	if newCount, err := a.checkForNew(); err != nil {
		log.Warnf("%v: got error checking for new: %v", a.Name, err)
	} else if newCount != 0 {
		a.NewMail(newCount)
	} else {
		log.Tracef("%v: CheckForNew returns 0", a.Name)
	}
}

func (a *Account) Idle() {

	if a.stopc != nil {
		log.Panicf("%v: a.stopc non-nil", a.Name)
	}

	log.Debugf("%v: Starting to IDLE", a.Name)

	a.stopc = make(chan struct{})        // Our channel to stop the command
	a.donec = make(chan error, 1)        // Our channel to here that the command completed
	a.updatec = make(chan client.Update) // Our channel to receive updates on
	a.c.Updates = a.updatec
	a.t = time.NewTimer(IdleTimeout) // Timer for refreshing the command

	// Run the command
	go func() {
		res := &Response{
			Stop:      a.stopc,
			RepliesCh: make(chan []byte, 10),
		}
		log.Tracef("%s: go-idle: Executing", a.Name)
		if status, err := a.c.Execute(&Command{}, res); err != nil {
			log.Tracef("%s: go-idle: Sending error: %v", a.Name, err)
			a.donec <- err
		} else {
			log.Tracef("%s: go-idle: Sending status: %v", a.Name, status)
			a.donec <- status.Err()
		}
	}()
}

func (a *Account) StopIdle(drain bool) {
	log.Debugf("%v: stopping IDLE", a.Name)

	if a.t != nil {
		if !a.t.Stop() {
			<-a.t.C
		}
		a.t = nil
	}

	close(a.stopc)
	a.stopc = nil

	if a.donec != nil {
		if drain {
			<-a.donec
		}
		close(a.donec)
		a.donec = nil
	}

	if a.updatec != nil {
		a.c.Updates = nil
		close(a.updatec)
		a.updatec = nil
	}

}

func (a *Account) NewMail(count int) {
	if count == 0 {
		log.Debugf("%v: signaling FULL update", a)
		a.eventc <- Event{FullUpdateEvent, a}
	} else {
		log.Debugf("%v: signaling NEW mail: %d", a, count)
		a.eventc <- Event{NewMailEvent, a}
	}
}

// Online configures the account to go online and attempt to stay that way.
// Errors connecting will be logged and retried after some delay
func (a *Account) Online(c chan Event) {
	if a.eventc != nil {
		log.Fatalf("%v: Account already online", a.Name)
	}

	a.eventc = c

	log.Debugf("%v: Taking online\n", a.Name)

	var err error
	for {
		if a.c == nil {
			if err := a.Login(); err != nil {
				log.Warnf("%v: login failed will retry: %v", a.Name, err)
			}
		}
		if a.c == nil {
			// No connnect, wait, then try and reconnect
			a.PollPause()
			continue
		} else if !a.idleOk {
			// No IDLE, wait, then check for new messages
			a.PollPause()
			a.CheckForNew()
			continue
		} else if a.stopc == nil {
			// If we have a client, but we are not IDLEing, start that.
			if _, err := a.selectInbox(); err != nil {
				// On error, logout, pause and try again
				log.Warnf("%v: got error selecting INBOX reconnecting: %v", a.Name, err)
				a.Logout()
				a.PollPause()
				continue
			}
			// Enable IDLE
			a.Idle()
		}

		log.Tracef("%v: Selecting", a)

		select {
		case u := <-a.updatec:
			if mu, ok := u.(*client.MailboxUpdate); ok {
				newCount := int(mu.Mailbox.Messages) - a.MsgCount
				a.MsgCount = int(mu.Mailbox.Messages)
				if newCount != 0 {
					a.NewMail(newCount)
				}
			} else {
				log.Debugf("%v: got Unknown update: %v", a, u)
			}
		case err = <-a.donec:
			// Since we didn't ask for this it probably means the
			// connection is lost.
			log.Debugf("%v: IDLE has stopped: %v", a.Name, err)
			a.Logout()
		case <-a.t.C:
			// Time to re-issue the command.
			log.Debugf("%v IDLE refresh", a.Name)
			a.t = nil // we're done with this timer.
			a.StopIdle(true)
		}
		log.Tracef("%v: out of select", a.Name)
	}
}
