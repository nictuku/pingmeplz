/*
Copyright 2011 Google Inc.

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

/*
webmon is a simple website monitoring program.

It reads a JSON-formatted rule file like this:

[
	{"Host": "example.com", "Email": "admin@example.net"}
]

It periodically makes a GET request to http://example.com/.
If the request returns anything other than a 200 OK response, it sends an email
to admin@example.net. When the request starts returning 200 OK again, it sends
another email.

Usage of webmon:
  -errors=3: number of errors before notifying
  -from="webmon@localhost": notification from address
  -Hosts="": host definition file
  -poll=10s: file poll interval
  -smtp="localhost:25": SMTP server
  -timeout=10s: response read timeout

webmon was written by Andrew Gerrand <adg@golang.org>
*/
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"
)

var (
	hostFile     = flag.String("hosts", "", "host definition file")
	pollInterval = flag.Duration("poll", time.Second*10, "file poll interval")
	fromEmail    = flag.String("from", "webmon@localhost", "notification from address")
	mailServer   = flag.String("smtp", "localhost:25", "SMTP server")
	numErrors    = flag.Int("errors", 3, "number of errors before notifying")
	readTimeout  = flag.Duration("timeout", time.Second*10, "response read timeout")
)

var runner *Runner

func main() {
	flag.Parse()
	runner = StartRunner(*hostFile, *pollInterval)

	http.HandleFunc("/", welcome)
	http.HandleFunc("/newhost", newhost)

	log.Panic(http.ListenAndServe(":8080", nil))
}

type Runner struct {
	sync.Mutex // Protects errors during concurrent Ping
	last       time.Time
	Hosts      []*Host
	errors     map[string]*State
}

type Host struct {
	Host  string
	Email string

	Error []error
}

type State struct {
	err  []error
	sent bool
}

func getWithTimeout(u string, timeout time.Duration) (*http.Response, error) {
	c := &http.Client{Transport: &http.Transport{
		Dial: func(n, addr string) (net.Conn, error) {
			c, err := net.Dial(n, addr)
			if err != nil {
				return nil, err
			}
			err = c.SetReadDeadline(time.Now().Add(timeout))
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}}
	return c.Get(u)
}

func (r *Runner) Ping(h *Host) error {
	u := fmt.Sprintf("http://%s/", h.Host)
	resp, err := getWithTimeout(u, *readTimeout)
	if err != nil {
		return r.Fail(h, err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return r.Fail(h, errors.New(resp.Status))
	}
	return r.OK(h)
}

func (r *Runner) OK(h *Host) error {
	r.Lock()
	s := r.errors[h.Host]
	if s == nil {
		r.Unlock()
		return nil
	}
	r.errors[h.Host] = nil
	r.Unlock()
	if !s.sent {
		return nil
	}
	h.Error = nil
	return h.Notify()
}

func (r *Runner) Fail(h *Host, getErr error) error {
	r.Lock()
	s := r.errors[h.Host]
	if s == nil {
		s = new(State)
		r.errors[h.Host] = s
	}
	r.Unlock()
	s.err = append(s.err, getErr)
	if s.sent || len(s.err) < *numErrors {
		return nil
	}
	s.sent = true
	h.Error = s.err
	return h.Notify()
}

func (r *Runner) NewHost(h *Host) error {
	r.Lock()
	defer r.Unlock()
	r.Hosts = append(r.Hosts, h)

	//func OpenFile(name string, flag int, perm FileMode) (file *File, err error)

	f, err := os.OpenFile(*hostFile, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("NewHost Open: %v", err)
	}
	defer f.Close()

	err = json.NewEncoder(f).Encode(r.Hosts)
	if err != nil {
		return fmt.Errorf("loadRules json Encode: %v", err)
	}
	return nil
}

var notifyTemplate = template.Must(template.New("").Funcs(template.FuncMap{
	"now": time.Now,
}).Parse(strings.TrimSpace(`
To: {{.Email}}
Subject: {{.Host}}

{{if .Error}}
{{.Host}} is down: {{range .Error}}{{.}}
{{end}}
{{else}}
{{.Host}} has come back up.
{{end}}
{{now}}
`)))

func (h *Host) Notify() error {
	var b bytes.Buffer
	err := notifyTemplate.Execute(&b, h)
	if err != nil {
		return err
	}
	log.Printf("%v down. Notifying %v", h.Host, h.Email)
	return SendMail(*mailServer, *fromEmail, []string{h.Email}, b.Bytes())
}

func StartRunner(file string, poll time.Duration) *Runner {
	r := &Runner{errors: make(map[string]*State)}
	go func() {
		for {
			if err := r.loadRules(file); err != nil {
				log.Println("StartRunner:", err)
			} else {
				errc := make(chan error)
				for i := range r.Hosts {
					go func(i int) {
						errc <- r.Ping(r.Hosts[i])
					}(i)
				}
				for _ = range r.Hosts {
					if err := <-errc; err != nil {
						log.Println(err)
					}
				}
			}
			time.Sleep(poll)
		}
	}()
	return r
}

func (r *Runner) loadRules(file string) error {
	fi, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("loadRules Stat: %v", err)
	}
	mtime := fi.ModTime()
	if mtime.Before(r.last) && r.Hosts != nil {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("loadRules Open: %v", err)
	}
	defer f.Close()
	var Hosts []*Host
	err = json.NewDecoder(f).Decode(&Hosts)
	if err != nil {
		return fmt.Errorf("loadRules json Decode: %v", err)
	}
	r.last = mtime
	r.Hosts = Hosts
	return nil
}

func SendMail(addr string, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	return c.Quit()
}
