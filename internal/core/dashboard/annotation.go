package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Region is a screenshot-pixel rectangle supplied by the dashboard canvas.
type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Annotation couples a human note with the exact frame and region it marks.
type Annotation struct {
	ID         int       `json:"id"`
	FrameID    uint64    `json:"frame_id"`
	Note       string    `json:"note"`
	Region     Region    `json:"region"`
	Screenshot string    `json:"screenshot"`
	CreatedAt  time.Time `json:"created_at"`
}

// AnnotationArtifact is written after each dashboard drawing. Screenshot paths
// are relative to the artifact so the JSON can be moved as one directory.
type AnnotationArtifact struct {
	Version     int          `json:"version"`
	Annotations []Annotation `json:"annotations"`
}

type annotationStore struct {
	mu       sync.Mutex
	path     string
	artifact AnnotationArtifact
}

func newAnnotationStore(path string) (*annotationStore, error) {
	store := &annotationStore{path: path, artifact: AnnotationArtifact{Version: 1, Annotations: []Annotation{}}}
	if err := store.saveLocked(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *annotationStore) add(note string, region Region, frameID uint64, frame []byte) (Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(frame) == 0 {
		return Annotation{}, fmt.Errorf("no viewport frame is available yet")
	}
	id := len(s.artifact.Annotations) + 1
	screenshot := fmt.Sprintf("annotation-%03d.jpg", id)
	path := filepath.Join(filepath.Dir(s.path), screenshot)
	if err := os.WriteFile(path, frame, 0o600); err != nil {
		return Annotation{}, err
	}
	annotation := Annotation{
		ID:         id,
		FrameID:    frameID,
		Note:       note,
		Region:     region,
		Screenshot: screenshot,
		CreatedAt:  time.Now().UTC(),
	}
	s.artifact.Annotations = append(s.artifact.Annotations, annotation)
	if err := s.saveLocked(); err != nil {
		return Annotation{}, err
	}
	return annotation, nil
}

func (s *annotationStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.artifact, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".annotations-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
