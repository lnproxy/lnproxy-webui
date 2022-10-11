package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"html/template"
	"image/color"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/acme/autocert"
)

var (
	lnproxyClient = &http.Client{}
	lnproxyHost   = flag.String("lnproxy-host", "127.0.0.1:4747", "REST host for lnproxy")
	httpPort      = flag.Int("http-port", 4748, "http port over which to expose web ui")
	httpsPort     = flag.Int("https-port", 4749, "http port over which to expose web ui")
)

func wrap(invoice string) (string, error) {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("http://%s/%s", *lnproxyHost, invoice),
		nil,
	)
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
	http.Redirect(w, r, r.URL.Path+"/"+invoice, http.StatusSeeOther)
}

var validPath = regexp.MustCompile("^/(wrap|api)/(lnbc[a-z0-9]+)$")

func wrapHandler(w http.ResponseWriter, r *http.Request) {
	m := validPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	i, err := wrap(m[2])
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
	m := validPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	i, err := wrap(m[2])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "%s\n", i)
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

var LND *http.Client

func main() {
	flag.Parse()

	certManager := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist("lnproxy.org", "www.lnproxy.org"),
		Cache:      autocert.DirCache("certs"),
	}

	http.Handle("/assets/", http.StripPrefix("/assets/", addNostrHeaders(http.FileServer(http.Dir("assets")))))
	http.Handle("/.well-known/", addNostrHeaders(http.StripPrefix("/.well-known/", http.FileServer(http.Dir("well-known")))))
	http.HandleFunc("/", xHandler("start"))
	http.HandleFunc("/about", xHandler("about"))
	http.HandleFunc("/doc", xHandler("doc"))
	http.HandleFunc("/contact", xHandler("contact"))
	http.HandleFunc("/wrap", redirectHandler)
	http.HandleFunc("/wrap/", wrapHandler)
	http.HandleFunc("/api/", apiHandler)

	server := &http.Server{
		Addr:      fmt.Sprintf("localhost:%d", *httpsPort),
		TLSConfig: certManager.TLSConfig(),
	}

	go http.ListenAndServe(fmt.Sprintf("localhost:%d", *httpPort), nil)
	log.Panicln(server.ListenAndServeTLS("", ""))
}
