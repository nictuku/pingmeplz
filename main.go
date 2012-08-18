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
	maxHosts     = flag.Int("maxHosts", 100, "Maximum number of hosts we should monitor")
)

const (
	// How many latency data points to keep for each host. 
	// This is the primary driver of memory usage.
	bufSize = 10080 // 7d worth of 1m frequency collections.
)

var runner *Runner

func main() {
	flag.Parse()
	runner = StartRunner(*hostFile, *pollInterval)

	http.HandleFunc("/", welcome)
	http.HandleFunc("/newhost", newhost)
	http.HandleFunc("/history", history)
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

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
	pos        int `json:"-"` // 0..9 

	// TODO: Optimize. Wasting too much memory.
	Latency        [bufSize]time.Duration `json:"-"`
	Error          [bufSize]error         `json:"-"`
	CollectionTime [bufSize]time.Time     `json:"-"`
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
	h.pos = (h.pos + 1) % bufSize
	h.Latency[h.pos] = duration
	h.CollectionTime[h.pos] = time.Now()
	log.Printf("latency for %d %v", h.pos, duration)
	h.Error[h.pos] = nil
	h.Unlock()
	return nil
}

func (r *Runner) Fail(h *Host, getErr error) error {
	h.Lock()
	h.pos = (h.pos + 1) % bufSize
	h.CollectionTime[h.pos] = time.Now()
	h.Error[h.pos] = getErr
	h.Unlock()
	return nil
}

func (r *Runner) save() error {
	// TODO: do a file switch only after the write is done.
	f, err := os.OpenFile(*hostFile, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("save Open: %v", err)
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
	if len(r.Hosts)+1 > *maxHosts {
		r.Unlock()
		return fmt.Errorf("Maximum number of monitored hosts reached: %d/%d", len(r.Hosts), *maxHosts)
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

	if len(r.Hosts) > *maxHosts {
		log.Printf("Warning: the configuration file at '%v' contains more hosts than what is set in -maxHosts.", file)
		log.Printf("Found %d hosts in the config, -maxHosts flag value is %d", len(r.Hosts), *maxHosts)
		log.Print("We will use the provided configuration and ignore the flag limit, so we will use more memory.")
	}

	tick := time.Tick(poll)

	go func() {
		for _ = range tick {
			errc := make(chan error)
			for _, h := range r.Hosts {
				go func(host *Host) {
					errc <- r.Ping(host)
				}(h)
			}
			for _ = range r.Hosts {
				if err := <-errc; err != nil {
					log.Println(err)
				}
			}
			r.save()
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
