package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBackupIsKeptOnTheServerAndCanBeDownloaded(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	// Nothing has been backed up yet.
	if list := getJSON(t, srv, "/api/backups"); len(list["backups"].([]any)) != 0 {
		t.Fatalf("backups = %v, want none yet", list["backups"])
	}

	res, err := srv.Client().Post(srv.URL+"/api/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var made map[string]any
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	name, _ := made["file"].(string)
	if !strings.HasPrefix(name, "hm-backup-") || !strings.HasSuffix(name, ".db") {
		t.Fatalf("file = %q", name)
	}

	// It stays on the server, and the list says how big it is.
	list := getJSON(t, srv, "/api/backups")
	backups, _ := list["backups"].([]any)
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want the one just made", list["backups"])
	}
	first, _ := backups[0].(map[string]any)
	if first["file"] != name {
		t.Errorf("listed %v, want %q", first["file"], name)
	}
	if size, _ := first["bytes"].(float64); size <= 0 {
		t.Errorf("bytes = %v, want the file's size", first["bytes"])
	}

	// And a copy can be downloaded, as a real SQLite file.
	got, err := srv.Client().Get(srv.URL + "/api/backups/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Body.Close() }()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("download gave %d", got.StatusCode)
	}
	if cd := got.Header.Get("Content-Disposition"); !strings.Contains(cd, name) {
		t.Errorf("disposition = %q, want the file named for saving", cd)
	}
	head := make([]byte, 16)
	if _, err := io.ReadFull(got.Body, head); err != nil {
		t.Fatal(err)
	}
	if string(head[:15]) != "SQLite format 3" {
		t.Errorf("downloaded %q, want a SQLite database", head)
	}
}

func TestABackupCanBeDeleted(t *testing.T) {
	srv, _ := searchFixture(t)

	res, err := srv.Client().Post(srv.URL+"/api/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var made map[string]any
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	name, _ := made["file"].(string)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/backups/"+name, nil)
	gone, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = gone.Body.Close()
	if gone.StatusCode != http.StatusOK {
		t.Fatalf("delete gave %d", gone.StatusCode)
	}

	// It is off the list and off the disk.
	if list := getJSON(t, srv, "/api/backups"); len(list["backups"].([]any)) != 0 {
		t.Errorf("backups = %v, want none left", list["backups"])
	}
	after, err := srv.Client().Get(srv.URL + "/api/backups/" + name)
	if err != nil {
		t.Fatal(err)
	}
	_ = after.Body.Close()
	if after.StatusCode != http.StatusNotFound {
		t.Errorf("download after delete gave %d, want 404", after.StatusCode)
	}

	// Deleting it again says so rather than pretending.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/backups/"+name, nil)
	twice, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = twice.Body.Close()
	if twice.StatusCode != http.StatusNotFound {
		t.Errorf("second delete gave %d, want 404", twice.StatusCode)
	}
}

func TestOnlyABackupCanBeDeleted(t *testing.T) {
	srv, _ := searchFixture(t)

	// The live database is not a backup, whatever the path says.
	for _, name := range []string{"hm.db", "..%2Fhm.db", "hm-backup-nonsense.db"} {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/backups/"+name, nil)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
			t.Errorf("deleting %s gave %d, want it refused", name, res.StatusCode)
		}
	}
	// And the database is still there.
	res, err := srv.Client().Get(srv.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func TestOnlyABackupCanBeDownloaded(t *testing.T) {
	srv, _ := searchFixture(t)

	// The name reaches the filesystem, so anything that is not exactly a
	// backup's name is refused rather than cleaned up and tried.
	for _, name := range []string{
		"hm.db",
		"..%2Fhm.db",
		"..%2F..%2Fetc%2Fpasswd",
		"hm-backup-20260920-093000.db.bak",
		"hm-backup-2026-09-20.db",
	} {
		res, err := srv.Client().Get(srv.URL + "/api/backups/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
			t.Errorf("%s gave %d, want it refused", name, res.StatusCode)
		}
	}
}

func TestAWriteToAnUnknownPathIsNotAnsweredWithThePage(t *testing.T) {
	srv, _ := searchFixture(t)

	// An old server meeting a newer browser: the route does not exist. It must
	// say so, not hand back index.html with a 200 that reads as success.
	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, "/api/backups/hm-backup-20260920-093000.db"},
		{http.MethodPost, "/api/something-new"},
		{http.MethodPut, "/api/whatever"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Errorf("%s %s gave 200: %s", tc.method, tc.path, body)
		}
		if strings.Contains(strings.ToLower(string(body)), "<!doctype") {
			t.Errorf("%s %s answered with the page: %s", tc.method, tc.path, body)
		}
	}

	// A page request still gets the page.
	res, err := srv.Client().Get(srv.URL + "/some/client/route")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusMethodNotAllowed {
		t.Errorf("a GET for a client-side route gave %d", res.StatusCode)
	}
}

func postBackup(t *testing.T, srv *httptest.Server) (int, map[string]any) {
	t.Helper()
	res, err := srv.Client().Post(srv.URL+"/api/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

func TestNoMoreThanThreeBackupsAreKept(t *testing.T) {
	srv, db := searchFixture(t)
	seedLogs(t, db)

	var names []string
	for range 3 {
		code, body := postBackup(t, srv)
		if code != http.StatusOK {
			t.Fatalf("backup gave %d: %v", code, body)
		}
		names = append(names, body["file"].(string))
	}
	if names[0] == names[1] || names[1] == names[2] {
		t.Fatalf("names = %v, want three separate files even within one second", names)
	}
	list := getJSON(t, srv, "/api/backups")
	if len(list["backups"].([]any)) != 3 || list["limit"] != float64(3) {
		t.Fatalf("list = %v, want three backups and the limit", list)
	}

	code, body := postBackup(t, srv)
	if code != http.StatusConflict || body["reason"] != "limit" {
		t.Fatalf("fourth backup gave %d %v, want it refused at the limit", code, body)
	}
	if !strings.Contains(body["error"].(string), "delete one") {
		t.Errorf("message = %q, want it to say how to make room", body["error"])
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/backups/"+names[0], nil)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if code, body := postBackup(t, srv); code != http.StatusOK {
		t.Fatalf("after deleting one, backup gave %d %v", code, body)
	}
}
