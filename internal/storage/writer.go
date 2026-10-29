package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/snowtoslow/asana-extractor/internal/asana"
)

// Snapshot supplies the data for users.json and projects.json from one extraction.
type Snapshot struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Users     []asana.User    `json:"users"`
	Projects  []asana.Project `json:"projects"`
}

type usersDocument struct {
	FetchedAt time.Time    `json:"fetched_at"`
	Users     []asana.User `json:"users"`
}

type projectsDocument struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Projects  []asana.Project `json:"projects"`
}

type tempFile interface {
	io.WriteCloser
	Sync() error
	Name() string
}

type pendingOutput struct {
	file        tempFile
	target      string
	isClosed    bool
	isPublished bool
}

// Writer publishes users.json and projects.json in one output directory.
// Each file replacement is atomic; the pair is not a filesystem transaction.
// Construct it with New.
type Writer struct {
	path       string
	createTemp func(string, string) (tempFile, error)
	rename     func(string, string) error
}

// New creates a writer for an output directory, which Write creates if missing.
func New(path string) *Writer {
	return &Writer{
		path: path,
		createTemp: func(dir, pattern string) (tempFile, error) {
			return os.CreateTemp(dir, pattern)
		},
		rename: os.Rename,
	}
}

// Write stages, syncs, and closes both documents before publishing users.json,
// then projects.json. Staging failures preserve both previous files. A failure
// between renames can leave complete files with different fetched_at timestamps.
func (w *Writer) Write(ctx context.Context, snapshot Snapshot) (err error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if w.path == "" {
		return errors.New("write snapshot: output directory is required")
	}
	if snapshot.Users == nil {
		snapshot.Users = []asana.User{}
	}
	if snapshot.Projects == nil {
		snapshot.Projects = []asana.Project{}
	}
	fetchedAt := snapshot.FetchedAt.UTC()
	// any is confined to this JSON encoding boundary; each document has a
	// concrete schema that contains only its own entity list and timestamp.
	documents := []struct {
		name  string
		value any
	}{
		{name: "users.json", value: usersDocument{FetchedAt: fetchedAt, Users: snapshot.Users}},
		{name: "projects.json", value: projectsDocument{FetchedAt: fetchedAt, Projects: snapshot.Projects}},
	}
	outputs := []pendingOutput{}
	defer func() {
		for _, output := range outputs {
			if !output.isClosed {
				if closeErr := output.file.Close(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("close temporary output: %w", closeErr))
				}
			}
			if !output.isPublished {
				removeErr := os.Remove(output.file.Name())
				if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, fmt.Errorf("remove temporary output: %w", removeErr))
				}
			}
		}
	}()

	if err := os.MkdirAll(w.path, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("prepare %s: %w", document.name, err)
		}
		data, err := json.MarshalIndent(document.value, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", document.name, err)
		}
		data = append(data, '\n')
		file, err := w.createTemp(w.path, "."+document.name+".tmp-*")
		if err != nil {
			return fmt.Errorf("create temporary %s: %w", document.name, err)
		}
		outputs = append(outputs, pendingOutput{file: file, target: filepath.Join(w.path, document.name)})
		output := &outputs[len(outputs)-1]
		if err := prepare(file, data); err != nil {
			return fmt.Errorf("prepare %s: %w", document.name, err)
		}
		err = file.Close()
		output.isClosed = true
		if err != nil {
			return fmt.Errorf("close temporary %s: %w", document.name, err)
		}
	}
	for i := range outputs {
		output := &outputs[i]
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("publish snapshot: %w", err)
		}
		if err := w.rename(output.file.Name(), output.target); err != nil {
			return fmt.Errorf("replace %s: %w", filepath.Base(output.target), err)
		}
		output.isPublished = true
	}
	return nil
}

func prepare(file tempFile, data []byte) error {
	n, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("write temporary output: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("write temporary output: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary output: %w", err)
	}
	return nil
}
