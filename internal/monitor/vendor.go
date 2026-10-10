package monitor

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/yude/tunwatch/internal/model"
)

//go:embed vendors.csv.gz
var bundledVendors []byte

type Vendors map[string]string

// LoadVendors accepts IEEE MA-L/MA-M/MA-S CSVs or a two-column prefix,vendor CSV.
func LoadVendors(path string) (Vendors, error) {
	v := Vendors{}
	z, err := gzip.NewReader(bytes.NewReader(bundledVendors))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	if err := readVendors(z, v); err != nil {
		return nil, err
	}
	if path == "" {
		return v, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := readVendors(f, v); err != nil {
		return nil, err
	}
	return v, nil
}

func readVendors(src io.Reader, v Vendors) error {
	r := csv.NewReader(io.LimitReader(src, 64<<20))
	r.FieldsPerRecord = -1
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if len(row) < 2 {
			continue
		}
		prefix, name := row[0], row[1]
		if len(row) >= 3 {
			prefix, name = row[1], row[2]
		}
		prefix = strings.ToUpper(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(prefix)))
		if prefix == "ASSIGNMENT" || prefix == "PREFIX" {
			continue
		}
		if len(prefix) != 6 && len(prefix) != 7 && len(prefix) != 9 {
			return errors.New("vendor prefix must have 6, 7 or 9 hex digits")
		}
		for _, c := range prefix {
			if !strings.ContainsRune("0123456789ABCDEF", c) {
				return errors.New("invalid vendor prefix")
			}
		}
		if len(name) > 256 {
			name = name[:256]
		}
		v[prefix] = name
	}
	return nil
}
func (v Vendors) Lookup(mac string) string {
	key := strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	if len(key) != 12 {
		return ""
	}
	for _, n := range []int{9, 7, 6} {
		if s := v[key[:n]]; s != "" {
			return s
		}
	}
	return ""
}
func osHint(o model.Observation) model.Hint {
	v := strings.ToLower(o.VendorClass)
	switch {
	case strings.Contains(v, "android"):
		return model.Hint{Value: "Android", Source: "dhcp.vendor_class", Confidence: "medium"}
	case strings.HasPrefix(v, "msft"):
		return model.Hint{Value: "Windows", Source: "dhcp.vendor_class", Confidence: "medium"}
	}
	// TTL is not a unique OS fingerprint. Expose the uncertainty, never an exact OS version.
	if o.TTL > 32 && o.TTL <= 64 {
		return model.Hint{Value: "Unix / mobile 系の可能性", Source: "ip.ttl", Confidence: "low"}
	}
	if o.TTL > 64 && o.TTL <= 128 {
		return model.Hint{Value: "Windows 系の可能性", Source: "ip.ttl", Confidence: "low"}
	}
	return model.Hint{}
}
