// Web UI for Andrew Gerrand's webmon.
package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net"
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
<!DOCTYPE html>
<html lang="en">
<head>
<title>PingMePlz - collaborative monitoring</title>


    <!-- Le styles -->
    <link href="/assets/css/bootstrap.min.css" rel="stylesheet">
    <style>
      body {
        padding-top: 60px; /* 60px to make the container go all the way to the bottom of the topbar */
      }
    </style>
    <link href="/assets/css/bootstrap-responsive.min.css" rel="stylesheet">

    <!-- Le HTML5 shim, for IE6-8 support of HTML5 elements -->
    <!--[if lt IE 9]>
      <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
    <![endif]-->

<script type="text/javascript">

  var _gaq = _gaq || [];
  _gaq.push(['_setAccount', 'UA-33925805-1']);
  _gaq.push(['_trackPageview']);

  (function() {
    var ga = document.createElement('script'); ga.type = 'text/javascript'; ga.async = true;
    ga.src = ('https:' == document.location.protocol ? 'https://ssl' : 'http://www') + '.google-analytics.com/ga.js';
    var s = document.getElementsByTagName('script')[0]; s.parentNode.insertBefore(ga, s);
  })();

</script>

</head>
<body>


    <div class="navbar navbar-fixed-top">
      <div class="navbar-inner">
        <div class="container">
          <a class="btn btn-navbar" data-toggle="collapse" data-target=".nav-collapse">
            <span class="icon-bar"></span>
            <span class="icon-bar"></span>
            <span class="icon-bar"></span>
            <span class="icon-bar"></span>
          </a>
          <a class="brand" href="#">PingMePlz</a>
          <div class="nav-collapse">
            <ul class="nav">
              <li class="active"><a href="/">Home</a></li>
              
              <li><a href="#">About</a></li>
            </ul>
          </div><!--/.nav-collapse -->
        </div>
      </div>
    </div>

 

<div class="hero-unit">
<h1>Collaborative web server monitoring</h1>

<label class="help-block">
  Add the string <em>pingmeplz.com</em> somewhere to your site's <em>robots.txt</em> file, then enter the host name below.
</label>

<form action="/newhost" method="post">

<div class="control-group">
          <label class="control-label" for="inputIcon">Host</label>
          <div class="controls">
            <div class="input-prepend">
              <span class="add-on"><i class="icon-globe"></i></span><input class="span4" name="host" type="text"
              	placeholder="example.com">
            </div>
          </div>
        </div>

<div class="control-group">
          <label class="control-label" for="inputIcon">Email</label>
          <div class="controls">
            <div class="input-prepend">
              <span class="add-on"><i class="icon-envelope"></i></span><input class="span4" name="email" type="text"
              	placeholder="you@example.com">
            </div>
          </div>
        </div>
<input type="submit" value="Submit" class="btn btn-primary">
</form>
</div>

<div class="row">
<div class="span10 offset1">

{{ if .Hosts }}
<h2>Sites being monitored</h2>
<p>Click to see the history</p>
<table class="table table-bordered">
<thead>
	<tr>
		<td>Hostname</td>
		<td>Latency in milliseconds</td>
	</tr>
</thread>
  {{range .Hosts }}	
    <tr>
    	<td><a href="/history?host={{ .Host }}">{{ .Host }}</a></td>
    	<td>{{ .Status }}</td>
	</tr>
  {{end }}
</table>
{{ else }}
<h2>No web sites being monitored.
{{ end }}
<h2>Note</h2>
<p>The reported latency includes the time for doing a <em>GET</em> request on /, reading the response and following any redirects (301, 302).</p>
</div>
</div>
</body>
</html>
`))

func newhost(w http.ResponseWriter, r *http.Request) {
	logreq(r)
	// Check required form arams.
	for _, v := range []string{"host", "email"} {
		if fv := r.FormValue(v); fv == "" {
			http.Error(w, v+" not specified or invalid", http.StatusBadRequest)
			return
		}
	}
	host := &Host{Host: r.FormValue("host"), Email: r.FormValue("email")}

	// Ensure this is a host address and not something more dodgy
	// such as fatdownloads.com/supershugefile.zip
	if ips, err := net.LookupIP(host.Host); err != nil || len(ips) == 0 {
		s := fmt.Sprintf("Host could not be resolved")
		log.Println(s)
		http.Error(w, s, http.StatusBadRequest)
		return
	}
	// TODO(nictuku): Don't download big files and blacklist a host that asks me to.

	// Check that they added "pingmeplz.com" to robots.txt, as a way 
	// to say they authorized this monitoring.
	u := fmt.Sprintf("http://%s/robots.txt", host.Host)
	resp, err := getWithTimeout(u, *readTimeout)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil || resp == nil || resp.StatusCode != 200 {
		s := fmt.Sprintf("newhost GET error: %v", err)
		log.Println(s)
		http.Error(w, s, http.StatusInternalServerError)
		return
	}

	bs, err := ioutil.ReadAll(resp.Body)
	if !bytes.Contains(bs, []byte("pingmeplz.com")) {
		s := fmt.Sprintf("Please add pingmeplz.com somewhere in http://%v/robots.txt "+
			"(for example, in a comment line), as a proof that you own this domain and want it to be monitored", host.Host)
		log.Print(s)
		http.Error(w, s, http.StatusBadRequest)
		return
	}
	log.Println("found the string pingmeplz.com in the robots.txt file for", host.Host)

	// Prerequisites are good, now add it to the config.
	if err := runner.NewHost(host); err != nil {
		log.Printf("newhost error: %v", err)
		http.Error(w, "Could not include new host: "+host.Host, http.StatusInternalServerError)
		return
	}

	s := fmt.Sprintf("Added host: %v", host.Host)
	log.Println(s)
	fmt.Fprint(w, s)
}
