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
    <li>{{ .Host }}</li>
  {{end }}
</ul>
{{ end }}
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
	if err := runner.NewHost(host); err != nil {
		log.Printf("newhost error: %v", err)
		http.Error(w, "Could not include new host in the runner.", http.StatusInternalServerError)
		return
	}
	s := fmt.Sprintf("Added host: %v", host)
	log.Println(s)
	fmt.Fprint(w, s)
}
