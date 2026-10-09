package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func event(id string, at time.Time) model.Event {
	return model.Event{ID: id, Time: at, Type: "online", Device: model.Device{ID: "eth0//00:11:22:33:44:55", MAC: "00:11:22:33:44:55", LastSeen: at, Status: "online"}}
}
func TestJournalRecoveryCursorAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := event("001", now)
	if err = s.Append(first); err != nil {
		t.Fatal(err)
	}
	if err = s.Save([]model.Device{first.Device}, now); err != nil {
		t.Fatal(err)
	}
	second := event("002", now.Add(time.Second))
	second.Device.Status = "offline"
	s.Append(second)
	if _, err = Open(dir, 30, 1); err == nil {
		t.Fatal("accepted concurrent writer")
	}
	s.Close()
	s, err = Open(dir, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Load()
	if err != nil || restored[first.Device.ID].Status != "offline" {
		t.Fatalf("replay failed: %+v %v", restored, err)
	}
	events, err := s.Query("002", first.Device.ID, 10)
	if err != nil || len(events) != 1 || events[0].ID != "001" {
		t.Fatal("cursor query failed")
	}
	events, _ = s.Query("", "different", 10)
	if len(events) != 0 {
		t.Fatal("device filter leaked unrelated events")
	}
}
func TestInterruptedFinalAppendIsRepaired(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 30, 1)
	s.Append(event("001", time.Now()))
	files, _ := s.files()
	s.Close()
	f, _ := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0600)
	f.Write([]byte{0x1f, 0x8b, 8, 0})
	f.Close()
	s, err := Open(dir, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Append(event("002", time.Now())); err != nil {
		t.Fatal(err)
	}
	events, err := s.Query("", "", 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("repair: %v %v", events, err)
	}
}
func TestCorruptionIsNotSilentlyDiscarded(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 30, 1)
	s.Append(event("001", time.Now()))
	files, _ := s.files()
	s.Close()
	b, _ := os.ReadFile(files[0])
	b[len(b)-8] ^= 0xff
	os.WriteFile(files[0], b, 0600)
	if s, err := Open(dir, 30, 1); err == nil {
		s.Close()
		t.Fatal("ignored CRC corruption")
	}
}
func TestRetentionWithoutNewTraffic(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, 1, 1)
	defer s.Close()
	s.Append(event("001", time.Now()))
	files, _ := s.files()
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(files[0], old, old)
	if err := s.Maintain(time.Now()); err != nil {
		t.Fatal(err)
	}
	files, _ = s.files()
	if len(files) != 0 {
		t.Fatal("expired final segment retained")
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); err != nil {
		t.Fatal("lock deleted")
	}
}
