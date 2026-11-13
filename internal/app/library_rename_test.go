package app

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryRenameAfterChildMutationAndScan(t *testing.T) {
	for _, mutation := range []string{"delete", "rename", "add"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			c := newContractClient(t)
			folder := filepath.Join(c.app.Library.Root, "folder")
			if err := os.Mkdir(folder, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, "video.mp4"), []byte("video fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Ensure child mutations change the directory metadata on every filesystem.
			before := time.Unix(946684800, 0)
			if err := os.Chtimes(folder, before, before); err != nil {
				t.Fatal(err)
			}
			refresh := func(id int64) {
				t.Helper()
				if err := c.app.Library.Refresh(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			type entryValidator struct {
				ID   int64  `json:"id"`
				ETag string `json:"etag"`
			}
			readEntry := func(parent int64) entryValidator {
				t.Helper()
				response := c.call(contractRequest{method: http.MethodGet, path: fmt.Sprintf("/api/directories/%d/entries", parent)})
				requireContractStatus(t, response, http.StatusOK)
				var page struct {
					Items []entryValidator `json:"items"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.Items[0].ETag == "" {
					t.Fatalf("got %s, want one entry with an ETag", response.Body.String())
				}
				return page.Items[0]
			}
			refresh(1)
			dir := readEntry(1)
			refresh(dir.ID)
			child := readEntry(dir.ID)
			switch mutation {
			case "delete":
				response := c.call(contractRequest{method: http.MethodDelete, path: fmt.Sprintf("/api/entries/%d", child.ID), tag: child.ETag, body: `{"confirmed":true}`})
				requireContractStatus(t, response, http.StatusNoContent)
			case "rename":
				response := c.call(contractRequest{method: http.MethodPost, path: fmt.Sprintf("/api/entries/%d/rename", child.ID), tag: child.ETag, body: `{"name":"renamed.mp4"}`})
				requireContractStatus(t, response, http.StatusCreated)
			case "add":
				if err := os.WriteFile(filepath.Join(folder, "added.mp4"), []byte("added fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Returning to the parent shows its existing index before the scan finishes.
			dir = readEntry(1)
			refresh(1)
			info, err := os.Stat(folder)
			if err != nil {
				t.Fatal(err)
			}
			current, err := c.app.Library.Get(t.Context(), dir.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.ModifiedNS != info.ModTime().UnixNano() || current.Size != info.Size() || info.ModTime().Equal(before) {
				t.Fatalf("got indexed metadata %+v, want updated size %d and mtime %v", current, info.Size(), info.ModTime())
			}
			request := contractRequest{method: http.MethodPost, path: fmt.Sprintf("/api/entries/%d/rename", dir.ID), tag: dir.ETag, body: `{"name":"renamed-folder"}`}
			requireContractStatus(t, c.call(request), http.StatusCreated)
			renamed := filepath.Join(c.app.Library.Root, "renamed-folder")
			files, err := os.ReadDir(renamed)
			if err != nil {
				t.Fatal(err)
			}
			wantNames := map[string][]string{"delete": {}, "rename": {"renamed.mp4"}, "add": {"added.mp4", "video.mp4"}}[mutation]
			if len(files) != len(wantNames) {
				t.Fatalf("got %d files, want %v", len(files), wantNames)
			}
			for i, name := range wantNames {
				if files[i].Name() != name {
					t.Fatalf("got file %q, want %q", files[i].Name(), name)
				}
			}
			// A real directory rename must still invalidate the previous validator.
			request.body = `{"name":"stale-rename"}`
			requireContractStatus(t, c.call(request), http.StatusPreconditionFailed)
			if _, err := os.Stat(renamed); err != nil {
				t.Fatalf("stale request changed the directory: %v", err)
			}
		})
	}
}
