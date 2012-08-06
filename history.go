package main

import (
	"html/template"
	"log"
	"net/http"
)

func history(w http.ResponseWriter, r *http.Request) {
	logreq(r)
	v := "host"
	host := r.FormValue(v)
	h, ok := runner.Hosts[host]
	if host == "" || !ok {
		http.Error(w, v+" not specified or invalid", http.StatusBadRequest)
		return
	}
	if err := historyTmpl.Execute(w, h); err != nil {
		http.Error(w, "oops", http.StatusInternalServerError)
		log.Println("history error:", err)
	}
}

var historyTmpl = template.Must(template.New("").Parse(`
<!--
You are free to copy and use this sample in accordance with the terms of the
Apache license (http://www.apache.org/licenses/LICENSE-2.0.html)
-->

<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title> {{ .Host }} </title>
  <meta http-equiv="content-type" content="text/html; charset=utf-8" />
  <script type="text/javascript" src="http://www.google.com/jsapi"></script>
  <script type="text/javascript">
    google.load('visualization', '1', {packages: ['annotatedtimeline']});
    function drawVisualization() {
      var data = new google.visualization.DataTable();
      data.addColumn('date', 'Date');
      data.addColumn('number', '{{ .Host }} Latency (seconds)');
      data.addColumn('string', 'title1');
      data.addColumn('string', 'text1');
      data.addRows([
        {{ range $index, $latency := .Latency }}
          {{ if $latency }}
            [new Date({{ index $.CollectionTime $index }}), {{ $latency.Seconds }}, null, null],
          {{ end }}
        {{ end }}
      ]);
    
      var annotatedtimeline = new google.visualization.AnnotatedTimeLine(
          document.getElementById('visualization'));
      annotatedtimeline.draw(data, {'displayAnnotations': true});
    }
    
    google.setOnLoadCallback(drawVisualization);
  </script>
</head>
<body style="font-family: Arial;border: 0 none;">
<div id="visualization" style="width: 800px; height: 400px;"></div>
</body>
</html>
`))
