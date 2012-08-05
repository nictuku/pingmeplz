// Web UI for Andrew Gerrand's webmon.
package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
)

func logreq(r *http.Request) {
	log.Printf("%v - %v", r.URL, r.RemoteAddr)
}

func welcome(w http.ResponseWriter, r *http.Request) {
	logreq(r)
	// This is also the default handler for the server.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	runner.Lock()
	defer runner.Unlock()
	if err := welcomeTmpl.Execute(w, runner); err != nil {
		http.Error(w, "oops", http.StatusInternalServerError)
		log.Println("welcome error:", err)
	}

}

var welcomeTmpl = template.Must(template.New("").Parse(`
<html><head><title>PingMePlz - collaborative monitoring</title></head>
<body>
<h1>Collaborative Web Monitoring</h1>

<h2>Monitor a new site</h2>
<p>
  Add the string <em>pingmeplz.com</em> somewhere to your site's <em>robots.txt</em> file, then enter the host name in the form below.
</p>
<form action="/newhost" method="post">
Host: <input name="host" type="text"><br>
Email: <input name="email" type="text"><br>
<input type="submit" value="Submit">
</form>

{{ if .Hosts }}
<h2>Web sites currently being monitored</h2>
<ul>
  {{range .Hosts }}	
    <li>{{ .Host }}, {{ .Status }} </li>
  {{end }}
</ul>
{{ else }}
<h2>No web sites being monitored.
{{ end }}
<h2>Notes</h2>
<p>The latency reported includes the time doing a GET request on /, receiving the response and following any redirects (301, 302).</p>
</body>
</html>
`))

func newhost(w http.ResponseWriter, r *http.Request) {
	logreq(r)
	for _, v := range []string{"host", "email"} {
		if fv := r.FormValue(v); fv == "" {
			http.Error(w, v+" not specified or invalid", http.StatusBadRequest)
			return
		}
	}
	host := &Host{Host: r.FormValue("host"), Email: r.FormValue("email")}

	u := fmt.Sprintf("http://%s/", host.Host)
	resp, err := getWithTimeout(u, *readTimeout)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil || resp == nil || resp.StatusCode != 200 {
		s := fmt.Sprintf("newhost GET error: %v", err)
		log.Println(s)
		http.Error(w, s, http.StatusInternalServerError)
		return
	}
	if err := runner.NewHost(host); err != nil {
		log.Printf("newhost error: %v", err)
		http.Error(w, "Could not include new host: "+host.Host, http.StatusInternalServerError)
		return
	}

	s := fmt.Sprintf("Added host: %v", host)
	log.Println(s)
	fmt.Fprint(w, s)
}
