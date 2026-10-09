// Package store persists only inventory metadata and state transitions, never packets.
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yude/tunwatch/internal/model"
	"golang.org/x/sys/unix"
)

const segmentSize = 1 << 20

type Store struct {
	mu   sync.Mutex
	dir  string
	days int
	max  int64
	lock *os.File
}

func Open(dir string, days, maxMB int) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("data directory is already in use")
	}
	s := &Store{dir: dir, days: days, max: int64(maxMB) << 20, lock: f}
	// Each append is a complete gzip member. Repair only an interrupted final append.
	files, _ := s.files()
	for i, p := range files {
		_, valid, e := readSegment(p)
		if e != nil {
			if i != len(files)-1 || (!errors.Is(e, io.ErrUnexpectedEOF) && !errors.Is(e, io.EOF)) {
				s.Close()
				return nil, fmt.Errorf("history corruption in %s: %w", filepath.Base(p), e)
			}
			if e = os.Truncate(p, valid); e != nil {
				s.Close()
				return nil, e
			}
		}
	}
	if err = s.prune(time.Now()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func (s *Store) files() ([]string, error) {
	return filepath.Glob(filepath.Join(s.dir, "events-*.jsonl.gz"))
}
func readSegment(path string) ([]model.Event, int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	r := bytes.NewReader(b)
	var out []model.Event
	var valid int64
	for r.Len() > 0 {
		z, e := gzip.NewReader(r)
		if e != nil {
			return out, valid, e
		}
		z.Multistream(false)
		raw, e := io.ReadAll(io.LimitReader(z, 128<<10))
		z.Close()
		if e != nil {
			return out, valid, e
		}
		var event model.Event
		if e = json.Unmarshal(raw, &event); e != nil {
			return out, valid, e
		}
		out = append(out, event)
		valid = int64(len(b) - r.Len())
	}
	return out, valid, nil
}
func compressed(v any) ([]byte, error) {
	var b bytes.Buffer
	z, _ := gzip.NewWriterLevel(&b, gzip.BestSpeed)
	if err := json.NewEncoder(z).Encode(v); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func (s *Store) Append(e model.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := compressed(e)
	if err != nil {
		return err
	}
	files, err := s.files()
	if err != nil {
		return err
	}
	path := ""
	// UTC date rotation and bounded compressed segments.
	if len(files) > 0 {
		last := files[len(files)-1]
		st, e2 := os.Stat(last)
		if e2 != nil {
			return e2
		}
		if st.Size()+int64(len(b)) <= segmentSize && strings.HasPrefix(filepath.Base(last), "events-"+e.Time.UTC().Format("20060102")) {
			path = last
		}
	}
	if path == "" {
		path = filepath.Join(s.dir, fmt.Sprintf("events-%s-%020d.jsonl.gz", e.Time.UTC().Format("20060102"), time.Now().UnixNano()))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return s.prune(time.Now())
}
func (s *Store) prune(now time.Time) error {
	files, err := s.files()
	if err != nil {
		return err
	}
	var total int64
	for _, p := range files {
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		total += st.Size()
	}
	for _, p := range files {
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		expired := st.ModTime().Before(now.Add(-time.Duration(s.days) * 24 * time.Hour))
		if expired || total > s.max {
			if e = os.Remove(p); e != nil {
				return e
			}
			total -= st.Size()
		}
	}
	return nil
}
func (s *Store) Maintain(now time.Time) error { s.mu.Lock(); defer s.mu.Unlock(); return s.prune(now) }
func (s *Store) Save(devices []model.Device, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := compressed(model.Snapshot{Time: now, Devices: devices})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir, "snapshot.json.gz")); err != nil {
		return err
	}
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) Load() (map[string]model.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	devices := map[string]model.Device{}
	var snap model.Snapshot
	f, err := os.Open(filepath.Join(s.dir, "snapshot.json.gz"))
	if err == nil {
		defer f.Close()
		z, e := gzip.NewReader(f)
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(z, 128<<20))
		z.Close()
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &snap); e != nil {
			return nil, e
		}
		for _, d := range snap.Devices {
			devices[d.ID] = d
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	files, err := s.files()
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		events, _, e := readSegment(p)
		if e != nil {
			return nil, e
		}
		for _, event := range events {
			if event.Time.After(snap.Time) {
				devices[event.Device.ID] = event.Device
			}
		}
	}
	return devices, nil
}

// Query returns newest first, with an exclusive event-ID cursor and optional device filter.
func (s *Store) Query(before, device string, limit int) ([]model.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.files()
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	out := []model.Event{}
	for _, p := range files {
		events, _, e := readSegment(p)
		if e != nil {
			return nil, e
		}
		for i := len(events) - 1; i >= 0; i-- {
			v := events[i]
			if (before == "" || v.ID < before) && (device == "" || v.Device.ID == device) {
				out = append(out, v)
				if len(out) == limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}
