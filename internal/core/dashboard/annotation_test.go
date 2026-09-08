package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestAnnotationStoreWritesFrameAndMachineReadableArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	store, err := newAnnotationStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	annotation, err := store.add("checkout button is clipped", Region{X: 10, Y: 20, Width: 30, Height: 40}, 7, []byte("jpeg"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if annotation.Screenshot != "annotation-001.jpg" || annotation.FrameID != 7 {
		t.Fatalf("screenshot = %q", annotation.Screenshot)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), annotation.Screenshot)); err != nil || string(data) != "jpeg" {
		t.Fatalf("screenshot not written: data=%q err=%v", data, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact AnnotationArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if artifact.Version != 1 || len(artifact.Annotations) != 1 || artifact.Annotations[0].Note != annotation.Note {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
}

func TestAnnotationEndpointStoresLatestFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	str := &streamer{}
	frameID := str.storeFrame([]byte("jpeg-frame"))
	srv, addr, err := newServer(0, str, Options{Annotate: true, ArtifactPath: path})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.serve(ctx)
	defer srv.httpSrv.Close()

	body := bytes.NewBufferString(fmt.Sprintf(`{"note":"button overlaps footer","frame_id":%d,"region":{"x":2,"y":3,"width":4,"height":5}}`, frameID))
	resp, err := http.Post(addr+"/annotations", "application/json", body)
	if err != nil {
		t.Fatalf("post annotation: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, data)
	}
	var got Annotation
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode annotation: %v", err)
	}
	if got.ID != 1 || got.FrameID != frameID || got.Note != "button overlaps footer" || got.Region.Width != 4 {
		t.Fatalf("unexpected endpoint annotation: %#v", got)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), got.Screenshot)); err != nil || string(data) != "jpeg-frame" {
		t.Fatalf("endpoint screenshot not written: data=%q err=%v", data, err)
	}

	artifactResp, err := http.Get(addr + "/annotations/artifact")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	defer artifactResp.Body.Close()
	if artifactResp.StatusCode != http.StatusOK {
		t.Fatalf("artifact status = %d", artifactResp.StatusCode)
	}
}

func TestAnnotationEndpointRejectsStaleFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	str := &streamer{}
	for i := 0; i <= retainedDashboardFrames; i++ {
		str.storeFrame([]byte{byte(i)})
	}
	srv, addr, err := newServer(0, str, Options{Annotate: true, ArtifactPath: path})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.serve(ctx)
	defer srv.httpSrv.Close()

	body := bytes.NewBufferString(`{"frame_id":1,"region":{"x":1,"y":1,"width":2,"height":2}}`)
	resp, err := http.Post(addr+"/annotations", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale frame status = %d, want 409", resp.StatusCode)
	}
}
