/*

Based on webmon by Andrew Gerrand <adg@golang.org>.

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

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	hostFile     = flag.String("hosts", "", "host definition file")
	pollInterval = flag.Duration("poll", time.Second*10, "file poll interval")
	readTimeout  = flag.Duration("timeout", time.Second*10, "response read timeout")
)

const (
	// How many errors and latency stats to keep for each host.
	bufSize = 30
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
	Hosts      map[string]*Host
}

type Host struct {
	Host  string
	Email string

	// Protects pos, Latency and Error
	sync.Mutex `json:"-"`
	pos        int                    `json:"-"` // 0..9 
	Latency    [bufSize]time.Duration `json:"-"`
	Error      [bufSize]error         `json:"-"`
}

func (h *Host) Status() string {
	h.Lock()
	defer h.Unlock()
	e := h.Error[h.pos]
	if e != nil {
		return "Error: " + e.Error()
	}
	fmt.Printf("%v %v\n", h.Host, h.Latency)
	return fmt.Sprintf("%dms", h.Latency[h.pos]/time.Millisecond)

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
	start := time.Now()
	resp, err := getWithTimeout(u, *readTimeout)
	duration := time.Since(start)
	if err != nil {
		log.Printf("%v FAIL after %v", h.Host, duration)
		return r.Fail(h, err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("%v ERROR after %v", h.Host, duration)
		return r.Fail(h, errors.New(resp.Status))
	}
	log.Printf("%v OK after %v", h.Host, duration)
	return r.OK(h, duration)
}

func (r *Runner) OK(h *Host, duration time.Duration) error {
	h.Lock()
	log.Printf("OKwas %v", h.pos)
	h.pos = (h.pos + 1) % 10
	log.Printf("OKnow %v", h.pos)
	h.Latency[h.pos] = duration
	log.Printf("latency for %d %v", h.pos, duration)
	h.Error[h.pos] = nil
	h.Unlock()
	return nil
}

func (r *Runner) Fail(h *Host, getErr error) error {
	h.Lock()
	log.Printf("FAILwas %v", h.pos)
	h.pos = (h.pos + 1) % 10
	log.Printf("FAILnow %v", h.pos)
	h.Error[h.pos] = getErr
	h.Unlock()
	return nil
}

func (r *Runner) save() error {
	// TODO: do a file switch only after the write is done.

	f, err := os.OpenFile(*hostFile, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("NewHost Open: %v", err)
	}
	defer f.Close()
	r.Lock()
	err = json.NewEncoder(f).Encode(r.Hosts)
	r.Unlock()
	if err != nil {
		return fmt.Errorf("loadRules json Encode: %v", err)
	}
	return nil
}
func (r *Runner) NewHost(h *Host) error {
	r.Lock()
	if h, ok := r.Hosts[h.Host]; ok {
		r.Unlock()
		return fmt.Errorf("Host already being monitored: %v", h.Host)
	}
	r.Hosts[h.Host] = h
	r.Unlock()

	r.save()
	return nil
}

func StartRunner(file string, poll time.Duration) *Runner {
	r := new(Runner)
	if err := r.loadRules(file); err != nil {
		log.Println("StartRunner:", err)
	}
	go func() {
		for {
			errc := make(chan error)
			for name, _ := range r.Hosts {
				go func() {
					h := r.Hosts[name]
					log.Println("pos", h.pos)
					errc <- r.Ping(h)
				}()
			}
			for _ = range r.Hosts {
				if err := <-errc; err != nil {
					log.Println(err)
				}
			}
			r.save()
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
	var Hosts map[string]*Host
	err = json.NewDecoder(f).Decode(&Hosts)
	if err != nil {
		return fmt.Errorf("loadRules json Decode: %v", err)
	}
	r.last = mtime
	r.Hosts = Hosts
	return nil
}
