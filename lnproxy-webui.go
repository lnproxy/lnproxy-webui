package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image/color"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

var (
	lnproxyClient    = &http.Client{}
	lnproxyURLString = flag.String("lnproxy-host", "http://127.0.0.1:4747/", "REST host for lnproxy")
	lnproxyURL       *url.URL
	httpPort         = flag.String("http-port", "4748", "http port over which to expose web ui")
)

var validPath = regexp.MustCompile("^/(?:wrap|api)/(?:(?:lightning:)?(?P<invoice>lnbc.*1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]+)|(?:LIGHTNING:)?(?P<invoice>LNBC.*1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]+$))")

func wrap(r *http.Request) (string, error) {
	m := validPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return "", fmt.Errorf("Invalid invoice")
	}
	u := *lnproxyURL
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequest("GET", u.JoinPath(strings.ToLower(m[1])).String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := lnproxyClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lnproxy error: %s", buf.String())
	}
	return buf.String(), nil
}

func QR(invoice string) string {
	q, err := qrcode.New(strings.ToUpper(invoice), qrcode.Medium)
	if err != nil {
		log.Panicln(err)
	}
	q.BackgroundColor = color.Transparent
	b, err := q.PNG(-8)
	if err != nil {
		log.Panicln(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

var templates = template.Must(template.ParseGlob("templates/*"))

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	invoice := r.FormValue("body")
	invoice = strings.TrimSpace(invoice)
	invoice = strings.ToLower(invoice)
	invoice = strings.TrimPrefix(invoice, "lightning:")
	if r.FormValue("advanced") == "on" {
		q := r.URL.Query()
		q.Set("routing_msat", r.FormValue("routing"))
		r.URL.RawQuery = q.Encode()
	}
	http.Redirect(w, r, r.URL.JoinPath(invoice).String(), http.StatusSeeOther)
}

func wrapHandler(w http.ResponseWriter, r *http.Request) {
	i, err := wrap(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = templates.ExecuteTemplate(w, "wrap",
		struct {
			Invoice string
			AsQR    string
		}{
			Invoice: i,
			AsQR:    QR(i),
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")

	format := r.URL.Query().Get("format")
	if format != "" && format != "json" {
		http.Error(w, `{"status": "ERROR", "reason":"Invalid format"}`, http.StatusBadRequest)
		return
	}

	i, err := wrap(r)
	if err != nil {
		b, _ := json.Marshal(struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}{
			Status: "ERROR",
			Reason: err.Error(),
		})
		http.Error(w, string(b), http.StatusInternalServerError)
		return
	}

	if format == "json" {
		fmt.Fprintf(w, "{\"wpr\":\"%s\"}", strings.TrimSpace(i))
	} else {
		fmt.Fprintf(w, "%s", i)
	}
}

func xHandler(x string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := templates.ExecuteTemplate(w, x, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func addNostrHeaders(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	}
}

func main() {
	flag.Parse()

	var err error
	lnproxyURL, err = url.Parse(*lnproxyURLString)
	if err != nil {
		fmt.Fprintf(flag.CommandLine.Output(), "Unable to parse lnproxy host url: %v\n", err)
		os.Exit(2)
	}

	http.Handle("/assets/", http.StripPrefix("/assets/", addNostrHeaders(http.FileServer(http.Dir("assets")))))
	http.Handle("/.well-known/", addNostrHeaders(http.StripPrefix("/.well-known/", http.FileServer(http.Dir("well-known")))))
	http.HandleFunc("/", xHandler("start"))
	http.HandleFunc("/wrap", redirectHandler)
	http.HandleFunc("/wrap/", wrapHandler)
	http.HandleFunc("/api/", apiHandler)

	log.Panicln(http.ListenAndServe(":"+*httpPort, nil))
}
