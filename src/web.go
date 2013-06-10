// Run a webserver to report stats

package main

import (
	"html/template"
	"log"
	"net"
	"net/http"
	"time"
)

type templateParams struct {
	Replies map[string]CapturedReply
	Uptime  string
	Peers   map[net.Conn]string
}

var startTime = time.Now()

func startWebServer() {
	t, err := template.New("mainTemplate").Parse(`
<html><body>
<h1>Iano's CSEtella Node</h1>

<h2>Stats</h2>
Uptime: {{.Uptime}}

<h2>Peers</h2>
<ul>
    {{range $_, $peer := .Peers}}
    <li>{{$peer}}</li>
    {{end}}
</ul>

<h2>Replies Observed</h2>
<table>
    <tr>
        <th>From</th>
        <th>When</th>
        <th>Text</th>
    </tr>
    {{range $_, $reply := .Replies}}
    <tr>
        <td>{{$reply.FromPeer}}</td>
        <td>{{$reply.TimeSeen}}</td>
        <td>{{$reply.Text}}</td>
    </tr>
    {{end}}
</table>

</body></html>`)
	if err != nil {
		log.Fatalln("Error compiling template: ", err)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := templateParams{
			Replies: repliesSeen,
			Uptime:  time.Now().Sub(startTime).String(),
			Peers:   conn2peer,
		}

		err = t.Execute(w, p)
	})

	log.Fatal(http.ListenAndServe(":20201", nil))
}
