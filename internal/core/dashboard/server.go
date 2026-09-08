package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/websocket"
)

//go:embed static
var staticFiles embed.FS

type server struct {
	httpSrv  *http.Server
	listener net.Listener
}

func newServer(port int, str *streamer, opts Options) (*server, string, error) {
	mux := http.NewServeMux()

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, "", err
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.Handle("/ws", websocket.Handler(func(conn *websocket.Conn) {
		ch := str.subscribe()
		defer str.unsubscribe(ch)
		for data := range ch {
			if _, err := conn.Write(data); err != nil {
				return
			}
		}
	}))
	if opts.Annotate {
		store, err := newAnnotationStore(opts.ArtifactPath)
		if err != nil {
			return nil, "", err
		}
		mux.HandleFunc("/annotations", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			defer r.Body.Close()
			var payload struct {
				Note    string `json:"note"`
				FrameID uint64 `json:"frame_id"`
				Region  Region `json:"region"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
				http.Error(w, "invalid annotation", http.StatusBadRequest)
				return
			}
			if payload.Region.Width <= 0 || payload.Region.Height <= 0 {
				http.Error(w, "annotation region must be non-empty", http.StatusBadRequest)
				return
			}
			frame, ok := str.frame(payload.FrameID)
			if !ok {
				http.Error(w, "annotation frame is stale or unavailable", http.StatusConflict)
				return
			}
			annotation, err := store.add(payload.Note, payload.Region, payload.FrameID, frame)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(annotation)
		})
		mux.HandleFunc("/annotations/artifact", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			http.ServeFile(w, r, opts.ArtifactPath)
		})
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	addr := fmt.Sprintf("http://%s", ln.Addr().String())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return &server{httpSrv: srv, listener: ln}, addr, nil
}

func (s *server) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.httpSrv.Close()
	}()
	_ = s.httpSrv.Serve(s.listener)
}
