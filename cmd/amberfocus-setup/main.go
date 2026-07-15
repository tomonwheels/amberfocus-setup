// Command amberfocus-setup is a standalone LoCo/localisation-correction filter
// calibration tool (a GPL derivative of frankl's locotest.c). It serves a local
// web UI, streams calibration test tones to a UPnP renderer, and exports the
// resulting FIR filters as .dbl files for import into amberDSP.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/tomonwheels/amberfocus-setup/internal/calib"
	"github.com/tomonwheels/amberfocus-setup/internal/upnp"
	"github.com/tomonwheels/amberfocus-setup/web"
)

var (
	engine = calib.New()

	mu        sync.Mutex
	renderers []upnp.Renderer
	selected  *upnp.Renderer

	outDir string
	port   int
)

func main() {
	flag.IntVar(&port, "port", 8099, "HTTP port for the web UI")
	flag.StringVar(&outDir, "out", ".", "output directory for exported .dbl filter files")
	noBrowser := flag.Bool("no-browser", false, "do not open a browser automatically")
	flag.Parse()

	go rescan()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/renderers", handleRenderers)
	mux.HandleFunc("/api/select", handleSelect)
	mux.HandleFunc("/api/locotest/start", handleStart)
	mux.HandleFunc("/api/locotest/stop", handleStop)
	mux.HandleFunc("/api/locotest/tone", handleTone)
	mux.HandleFunc("/api/locotest/mult", handleMult)
	mux.HandleFunc("/api/locotest/vol", handleVol)
	mux.HandleFunc("/api/locotest/stream", engine.Stream)
	mux.HandleFunc("/api/locotest/export", handleExport)

	url := fmt.Sprintf("http://localhost:%d", port)
	log.Printf("amberFOCUS Setup → %s   (filters → %s)", url, outDir)
	if !*noBrowser {
		go func() { time.Sleep(400 * time.Millisecond); openBrowser(url) }()
	}
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := web.FS.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func rescan() {
	rs, err := upnp.Discover(4 * time.Second)
	if err != nil {
		log.Printf("discover: %v", err)
		return
	}
	mu.Lock()
	renderers = rs
	if selected == nil && len(rs) > 0 {
		r := rs[0]
		selected = &r
	}
	mu.Unlock()
	log.Printf("%d renderer(s) found", len(rs))
}

func handleRenderers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("rescan") == "1" {
		rescan()
	}
	mu.Lock()
	rs := renderers
	var selUDN string
	if selected != nil {
		selUDN = selected.UDN
	}
	mu.Unlock()
	writeJSON(w, map[string]any{"renderers": rs, "selected": selUDN})
}

func handleSelect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UDN string `json:"udn"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	mu.Lock()
	for i := range renderers {
		if renderers[i].UDN == body.UDN {
			sel := renderers[i]
			selected = &sel
			break
		}
	}
	mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	sel := selected
	mu.Unlock()
	if sel == nil {
		http.Error(w, "kein Renderer ausgewählt", http.StatusBadRequest)
		return
	}
	engine.Start()
	streamURL := fmt.Sprintf("http://%s:%d/api/locotest/stream", localIP(), port)
	if err := upnp.SetAVTransportURI(sel, streamURL); err != nil {
		engine.Stop()
		http.Error(w, "SetAVTransportURI: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := upnp.Play(sel); err != nil {
		engine.Stop()
		http.Error(w, "Play: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	engine.Stop()
	mu.Lock()
	sel := selected
	mu.Unlock()
	if sel != nil {
		go func() { _ = upnp.Stop(sel) }()
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleTone(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Index  int  `json:"index"`
		Active bool `json:"active"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	engine.SetTone(b.Index, b.Active)
	writeJSON(w, map[string]any{"ok": true})
}

func handleMult(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Index int     `json:"index"`
		Mult  float64 `json:"mult"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	engine.SetMult(b.Index, b.Mult)
	writeJSON(w, map[string]any{"ok": true})
}

func handleVol(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Vol float64 `json:"vol"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	engine.SetVol(b.Vol)
	writeJSON(w, map[string]any{"ok": true})
}

func handleExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filters map[string]string `json:"filters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	count := 0
	for rateStr, b64 := range req.Filters {
		var rate int
		fmt.Sscanf(rateStr, "%d", &rate)
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		name := fmt.Sprintf("FHR-%s.dbl", rateLabel(rate))
		if err := os.WriteFile(filepath.Join(outDir, name), raw, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		count++
	}
	abs, _ := filepath.Abs(outDir)
	writeJSON(w, map[string]any{
		"count":   count,
		"message": fmt.Sprintf("%d Filter gespeichert in %s", count, abs),
	})
}

func rateLabel(rate int) string {
	if rate%1000 == 0 {
		return fmt.Sprintf("%d", rate/1000)
	}
	return fmt.Sprintf("%.1f", float64(rate)/1000)
}

func localIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
