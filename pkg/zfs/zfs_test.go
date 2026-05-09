package zfs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseDatasetShared(t *testing.T) {
	out := strings.Join([]string{
		"type\tfilesystem",
		"mountpoint\t/tank/foo",
		"mounted\tyes",
		ShareProperty + "\ton",
		"",
	}, "\n")
	ds, err := parseDataset("tank/foo", out)
	if err != nil {
		t.Fatalf("parseDataset: %v", err)
	}
	if ds.Type != "filesystem" {
		t.Errorf("Type = %q, want filesystem", ds.Type)
	}
	if ds.Mountpoint != "/tank/foo" {
		t.Errorf("Mountpoint = %q, want /tank/foo", ds.Mountpoint)
	}
	if !ds.Mounted {
		t.Error("Mounted = false, want true")
	}
	if !ds.IsShared() {
		t.Errorf("IsShared() = false (ShareValue=%q), want true", ds.ShareValue)
	}
}

func TestParseDatasetUnshared(t *testing.T) {
	out := strings.Join([]string{
		"type\tfilesystem",
		"mountpoint\t/tank/foo",
		"mounted\tno",
		ShareProperty + "\t-",
		"",
	}, "\n")
	ds, err := parseDataset("tank/foo", out)
	if err != nil {
		t.Fatalf("parseDataset: %v", err)
	}
	if ds.IsShared() {
		t.Errorf("IsShared() = true (ShareValue=%q), want false", ds.ShareValue)
	}
	if ds.Mounted {
		t.Error("Mounted = true, want false")
	}
}

func TestGetDatasetNotFound(t *testing.T) {
	c := &Client{run: func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte("cannot open 'tank/missing': dataset does not exist\n"), errors.New("exit status 1")
	}}
	_, err := c.GetDataset(context.Background(), "tank/missing")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("err = %v (%T), want *NotFoundError", err, err)
	}
}

func TestGetDatasetOtherError(t *testing.T) {
	c := &Client{run: func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte("cannot open 'tank/foo': permission denied\n"), errors.New("exit status 1")
	}}
	_, err := c.GetDataset(context.Background(), "tank/foo")
	var nfe *NotFoundError
	if errors.As(err, &nfe) {
		t.Fatalf("err = %v, want a non-NotFound error", err)
	}
	if err == nil {
		t.Fatal("err = nil, want error")
	}
}

func TestGetDatasetSuccess(t *testing.T) {
	c := &Client{run: func(ctx context.Context, args ...string) ([]byte, error) {
		// Sanity-check that we forward the property allowlist verbatim.
		want := "type,mountpoint,mounted," + ShareProperty
		var got string
		for _, a := range args {
			if strings.HasPrefix(a, "type,") {
				got = a
			}
		}
		if got != want {
			t.Errorf("zfs args properties = %q, want %q", got, want)
		}
		return []byte("type\tfilesystem\nmountpoint\t/tank/foo\nmounted\tyes\n" + ShareProperty + "\ton\n"), nil
	}}
	ds, err := c.GetDataset(context.Background(), "tank/foo")
	if err != nil {
		t.Fatalf("GetDataset: %v", err)
	}
	if !ds.IsShared() || !ds.Mounted || ds.Mountpoint != "/tank/foo" {
		t.Errorf("unexpected dataset: %+v", ds)
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"tank", true},
		{"tank/foo", true},
		{"tank/foo/bar.baz_qux-quux", true},
		{"tank:1/foo", true},
		{"TANK/Foo", true},
		{"", false},
		{"/tank", false},
		{"-tank", false},
		{"tank/..", false},
		{"tank/../bar", false},
		{"tank;rm -rf /", false},
		{"tank foo", false},
		{"tank\nfoo", false},
		{"tank$foo", false},
	}
	for _, c := range cases {
		err := validateName(c.name)
		if c.ok && err != nil {
			t.Errorf("validateName(%q) = %v, want nil", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateName(%q) = nil, want error", c.name)
		}
	}
}
