package engine

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/go-rod/rod"
)

const (
	maxDropFiles      = 20
	maxDropFileBytes  = 10 << 20
	maxDropTotalBytes = 25 << 20
)

// DropData is one text entry placed in a DataTransfer object.
// MIME is the DataTransfer format, for example text/plain or application/json.
type DropData struct {
	MIME  string `json:"mime"`
	Value string `json:"value"`
}

type dropFile struct {
	Name   string `json:"name"`
	MIME   string `json:"mime"`
	Base64 string `json:"base64"`
}

type dropPayload struct {
	Files []dropFile `json:"files"`
	Data  []DropData `json:"data"`
}

// DropTarget dispatches browser drag events on an arbitrary element. Unlike
// UploadRef, it does not require an <input type=file>: supplied files become
// File objects in event.dataTransfer and --data values become DataTransfer
// string entries.
func DropTarget(page *rod.Page, target string, paths []string, data []DropData, snapshot *PageSnapshot) error {
	if len(paths) == 0 && len(data) == 0 {
		return fmt.Errorf("drop: provide at least one file path or data entry")
	}
	if err := ActivePolicy.AllowAction("upload"); err != nil {
		return err
	}
	if len(paths) > maxDropFiles {
		return fmt.Errorf("drop: %d files exceeds the limit of %d", len(paths), maxDropFiles)
	}

	// Resolve before opening any file. An invalid or ambiguous target must not
	// trigger potentially expensive reads from disk.
	el, err := ResolveTarget(page, target, snapshot)
	if err != nil {
		return err
	}
	var totalBytes int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("drop: stat file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("drop: %q is not a regular file", path)
		}
		if info.Size() > maxDropFileBytes {
			return fmt.Errorf("drop: file %q is %d bytes; limit is %d", path, info.Size(), maxDropFileBytes)
		}
		totalBytes += info.Size()
		if totalBytes > maxDropTotalBytes {
			return fmt.Errorf("drop: files total %d bytes; limit is %d", totalBytes, maxDropTotalBytes)
		}
	}

	payload := dropPayload{
		Files: []dropFile{},
		Data:  append([]DropData(nil), data...),
	}
	if payload.Data == nil {
		payload.Data = []DropData{}
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("drop: read file %q: %w", path, err)
		}
		fileType := mime.TypeByExtension(filepath.Ext(path))
		if fileType == "" {
			fileType = "application/octet-stream"
		}
		payload.Files = append(payload.Files, dropFile{
			Name:   filepath.Base(path),
			MIME:   fileType,
			Base64: base64.StdEncoding.EncodeToString(contents),
		})
	}

	if err := DropElement(el, payload); err != nil {
		return err
	}
	_ = page.WaitStable(300 * time.Millisecond)
	return nil
}

func DropElement(el *rod.Element, payload dropPayload) error {
	_, err := el.Eval(`(payload) => {
		const transfer = new DataTransfer();
		for (const file of payload.files) {
			const binary = atob(file.base64);
			const bytes = Uint8Array.from(binary, char => char.charCodeAt(0));
			transfer.items.add(new File([bytes], file.name, { type: file.mime }));
		}
		for (const entry of payload.data) transfer.setData(entry.mime, entry.value);
		for (const type of ['dragenter', 'dragover', 'drop']) {
			this.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: transfer }));
		}
	}`, payload)
	if err != nil {
		return fmt.Errorf("drop: dispatch: %w", err)
	}
	return nil
}
