package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// File-only cupboard subcommands. None of these start the browser or take the profile lock.

const (
	colStock = iota
	colItem
	colQty
	colSource
	colConfirmed
	colSize
	colSizeQty
	colSizeUnit
	colStorage
	nCols
)

// cupboardFileOnly reports whether a `cupboard` invocation needs no browser.
func cupboardFileOnly(pos []string) bool { return len(pos) == 0 || pos[0] != "add-latest" }

type cupboardFile struct {
	path     string
	exists   bool
	raw      []byte
	rows     [][]string // padded to nCols, header excluded
	oldShape bool       // file had the 5-column header
}

func loadCupboard(path string) (*cupboardFile, error) {
	f := &cupboardFile{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	} else if err != nil {
		return nil, err
	}
	f.exists, f.raw = true, raw
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) > 0 && len(recs[0]) > 0 && recs[0][0] == "stockcode" {
		f.oldShape = len(recs[0]) <= oldCupboardCols
		recs = recs[1:]
	} else if len(recs) > 0 {
		return nil, errors.New("cupboard file has no recognisable header; not touching it")
	}
	for _, rec := range recs {
		row := make([]string, nCols)
		copy(row, rec)
		f.rows = append(f.rows, row)
	}
	return f, nil
}

// save writes rows atomically (temp file, rename) and keeps the previous file as <file>.bak.
func (f *cupboardFile) save() error {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write(cupboardHeader)
	w.WriteAll(f.rows)
	if err := w.Error(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	if f.exists {
		bak := f.path + ".bak"
		if f.oldShape {
			bak = backupPath(f.path) // never clobber the pre-upgrade copy
		}
		if err := os.WriteFile(bak, f.raw, 0o644); err != nil {
			return err
		}
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func rowObj(r []string) obj {
	o := obj{}
	for i, h := range cupboardHeader {
		o[h] = r[i]
	}
	return o
}

func rowsObj(rows [][]string) []any {
	out := []any{}
	for _, r := range rows {
		out = append(out, rowObj(r))
	}
	return out
}

func rowLabel(r []string) string {
	return fmt.Sprintf("%s %q (qty %s, %s)", r[colStock], r[colItem], r[colQty], r[colSource])
}

// find returns the indexes of rows matching key: an exact stockcode, else a case-insensitive
// exact item name, else a case-insensitive substring of the item name.
func (f *cupboardFile) find(key string) []int {
	key = strings.TrimSpace(key)
	var idx []int
	if key == "" {
		return nil
	}
	if _, err := strconv.ParseUint(key, 10, 64); err == nil {
		for i, r := range f.rows {
			if r[colStock] == key {
				idx = append(idx, i)
			}
		}
		if len(idx) > 0 {
			return idx
		}
	}
	lk := strings.ToLower(key)
	for i, r := range f.rows {
		if strings.ToLower(r[colItem]) == lk {
			idx = append(idx, i)
		}
	}
	if len(idx) > 0 {
		return idx
	}
	for i, r := range f.rows {
		if strings.Contains(strings.ToLower(r[colItem]), lk) {
			idx = append(idx, i)
		}
	}
	return idx
}

// findOne resolves key to exactly one row, or errors (listing candidates when ambiguous).
func (f *cupboardFile) findOne(key string) (int, error) {
	idx := f.find(key)
	switch len(idx) {
	case 0:
		return 0, fmt.Errorf("nothing in the cupboard matches %q", key)
	case 1:
		return idx[0], nil
	}
	var parts []string
	for _, i := range idx {
		parts = append(parts, rowLabel(f.rows[i]))
	}
	return 0, fmt.Errorf("%q matches %d rows, nothing changed; use the stockcode or a fuller name: %s", key, len(idx), strings.Join(parts, "; "))
}

func positiveInt(o opts, k string, def int) (int, error) {
	n, err := o.num(k, def)
	if err != nil {
		return 0, err
	}
	if n < 1 {
		return 0, usageErr("--%s must be at least 1", k)
	}
	return n, nil
}

func rowQty(r []string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(r[colQty]))
	if err != nil {
		return 0, fmt.Errorf("row %s has a non-numeric qty %q; fix it with set-qty", rowLabel(r), r[colQty])
	}
	return n, nil
}

// commit saves unless dry, and returns the common result fields.
func (f *cupboardFile) commit(dry bool, out obj) (obj, error) {
	out["success"], out["file"], out["dryRun"] = true, f.path, dry
	if !dry {
		if err := f.save(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func cmdCupboardFile(sub string, pos []string, o opts) (obj, error) {
	path := o.str("file", defaultCupboardFile())
	dry := o.str("dry-run", "") == "true"
	args := pos[1:]
	today := time.Now().Format("2006-01-02")

	f, err := loadCupboard(path)
	if err != nil {
		return nil, err
	}

	switch sub {
	case "list":
		search := strings.ToLower(o.str("search", ""))
		rows := [][]string{}
		for _, r := range f.rows {
			if search == "" || strings.Contains(strings.ToLower(r[colItem]), search) {
				rows = append(rows, r)
			}
		}
		return obj{"success": true, "file": path, "total": len(f.rows), "count": len(rows), "rows": rowsObj(rows)}, nil

	case "add":
		item := strings.TrimSpace(strings.Join(args, " "))
		if item == "" {
			return nil, usageErr("usage: woolies cupboard add <item...> [--qty N] [--source had|elsewhere|...] [--stockcode N] [--size 500g]")
		}
		qty, err := positiveInt(o, "qty", 1)
		if err != nil {
			return nil, err
		}
		code := o.str("stockcode", "")
		if code != "" {
			if _, err := strconv.ParseUint(code, 10, 64); err != nil {
				return nil, usageErr("--stockcode must be a number")
			}
		}
		source := strings.TrimSpace(o.str("source", "had"))
		if source == "" {
			return nil, usageErr("--source cannot be empty")
		}
		size := strings.TrimSpace(o.str("size", ""))
		var sq, su string
		if size != "" {
			q, u, ok := parsePackSize(size)
			if !ok {
				return nil, usageErr("--size %q not understood (try 500g, 1kg, 1L, 375ml, each, 6pk)", size)
			}
			sq, su = fmtNum(q), u
		}
		// Merge into an existing row: same stockcode (when given), else same item name.
		var match []int
		if code != "" {
			for i, r := range f.rows {
				if r[colStock] == code {
					match = append(match, i)
				}
			}
		} else {
			for i, r := range f.rows {
				if strings.EqualFold(r[colItem], item) {
					match = append(match, i)
				}
			}
		}
		if len(match) > 0 {
			pick := match[0]
			for _, i := range match { // prefer the row with the same source
				if f.rows[i][colSource] == source {
					pick = i
					break
				}
			}
			r := f.rows[pick]
			cur, err := rowQty(r)
			if err != nil {
				return nil, err
			}
			r[colQty], r[colConfirmed] = strconv.Itoa(cur+qty), today
			if r[colSize] == "" && size != "" {
				r[colSize], r[colSizeQty], r[colSizeUnit] = size, sq, su
			}
			return f.commit(dry, obj{"action": "merged", "note": fmt.Sprintf("already in the cupboard; added %d to its qty (%d -> %d)", qty, cur, cur+qty), "row": rowObj(r)})
		}
		r := make([]string, nCols)
		r[colStock], r[colItem], r[colQty], r[colSource], r[colConfirmed] = code, item, strconv.Itoa(qty), source, today
		r[colSize], r[colSizeQty], r[colSizeUnit] = size, sq, su
		f.rows = append(f.rows, r)
		return f.commit(dry, obj{"action": "added", "row": rowObj(r)})

	case "remove":
		if len(args) == 0 {
			return nil, usageErr("usage: woolies cupboard remove <item|stockcode>")
		}
		i, err := f.findOne(strings.Join(args, " "))
		if err != nil {
			return nil, err
		}
		gone := f.rows[i]
		f.rows = append(f.rows[:i:i], f.rows[i+1:]...)
		return f.commit(dry, obj{"action": "removed", "row": rowObj(gone), "remaining": len(f.rows)})

	case "use":
		if len(args) == 0 {
			return nil, usageErr("usage: woolies cupboard use <item|stockcode> [--qty N]")
		}
		n, err := positiveInt(o, "qty", 1)
		if err != nil {
			return nil, err
		}
		i, err := f.findOne(strings.Join(args, " "))
		if err != nil {
			return nil, err
		}
		r := f.rows[i]
		cur, err := rowQty(r)
		if err != nil {
			return nil, err
		}
		out := obj{}
		if n >= cur {
			if n > cur {
				out["note"] = fmt.Sprintf("used %d but only %d were recorded; row removed", n, cur)
			}
			f.rows = append(f.rows[:i:i], f.rows[i+1:]...)
			out["action"], out["row"], out["remaining"] = "used-up", rowObj(r), len(f.rows)
			return f.commit(dry, out)
		}
		r[colQty] = strconv.Itoa(cur - n)
		out["action"], out["row"] = "used", rowObj(r)
		return f.commit(dry, out)

	case "set-qty":
		if len(args) < 2 {
			return nil, usageErr("usage: woolies cupboard set-qty <item|stockcode> <N>")
		}
		n, err := strconv.Atoi(args[len(args)-1])
		if err != nil || n < 0 {
			return nil, usageErr("set-qty needs a whole number 0 or more as its last argument")
		}
		i, err := f.findOne(strings.Join(args[:len(args)-1], " "))
		if err != nil {
			return nil, err
		}
		r := f.rows[i]
		if n == 0 {
			f.rows = append(f.rows[:i:i], f.rows[i+1:]...)
			return f.commit(dry, obj{"action": "removed", "note": "qty 0 removes the row", "row": rowObj(r), "remaining": len(f.rows)})
		}
		r[colQty] = strconv.Itoa(n)
		return f.commit(dry, obj{"action": "set-qty", "row": rowObj(r)})

	case "clear":
		if len(args) > 0 {
			return nil, usageErr("usage: woolies cupboard clear [--dry-run] [--yes]")
		}
		if !dry && o.str("yes", "") != "true" {
			return nil, usageErr("clear empties the whole cupboard (%d rows). Re-run with --dry-run to preview or --yes to do it", len(f.rows))
		}
		gone := f.rows
		f.rows = nil
		if !f.exists {
			return obj{"success": true, "file": path, "dryRun": dry, "cleared": 0, "rows": []any{}}, nil
		}
		return f.commit(dry, obj{"action": "cleared", "cleared": len(gone), "rows": rowsObj(gone)})
	}
	return nil, usageErr("unknown cupboard subcommand %q. Use list, add, remove, use, set-qty, clear or add-latest", sub)
}
