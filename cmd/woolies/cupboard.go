package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var cupboardHeader = []string{"stockcode", "item", "qty", "source", "confirmed", "size", "size_qty", "size_unit", "storage"}

const oldCupboardCols = 5

// sizeInfo is the pack size and storage note recorded for a product. Empty fields mean unknown.
type sizeInfo struct {
	Size    string // as Woolies shows it, e.g. "700g"
	Qty     string // normalised number, e.g. "700"
	Unit    string // g, ml or each
	Storage string
	Source  string // "package" or "nutrition" (servings x serving size); informational
}

// cmdCupboard handles `woolies cupboard add-latest`. It only reads Woolies; the single write
// is to the CSV (skipped with --dry-run).
func cmdCupboard(pos []string, o opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("usage: woolies cupboard list|add|remove|use|set-qty|clear|add-latest (see `woolies help`)")
	}
	if pos[0] != "add-latest" {
		return cmdCupboardFile(pos[0], pos, o)
	}
	if len(pos) != 1 {
		return nil, usageErr("usage: woolies cupboard add-latest [--file <path>] [--dry-run]")
	}
	path := o.str("file", defaultCupboardFile())
	dry := o.str("dry-run", "") == "true"

	s, err := getSession()
	if err != nil {
		return nil, err
	}
	order, err := fetchLatestOrder(s)
	if err != nil {
		return nil, err
	}
	if order["success"] != true {
		return nil, errors.New("could not read the latest order's items; nothing written")
	}
	// Product lookups run one after another in this process (one browser profile user).
	var lookupErrs []any
	cache := map[string]sizeInfo{}
	lookup := func(code string) sizeInfo {
		if v, ok := cache[code]; ok {
			return v
		}
		info, err := fetchSizeInfo(code)
		if err != nil {
			lookupErrs = append(lookupErrs, obj{"stockcode": code, "error": err.Error()})
		}
		cache[code] = info
		return info
	}
	res, err := applyCupboard(path, order, time.Now().Format("2006-01-02"), dry, lookup)
	if err != nil {
		return nil, err
	}
	out := obj{
		"success": true, "file": path, "dryRun": dry,
		"orderId": order["orderId"], "status": order["status"],
		"addedCount": len(res.added), "skippedCount": len(res.skipped),
		"added": res.added, "skipped": res.skipped,
		"migration": obj{"needed": res.migrated, "backup": res.backup, "backfilled": res.backfilled},
	}
	if len(lookupErrs) > 0 {
		out["lookupErrors"] = lookupErrs
	}
	return out, nil
}

// fetchSizeInfo reads one product's detail page data.
func fetchSizeInfo(code string) (sizeInfo, error) {
	data, err := request(homeURL+"/apis/ui/product/detail/"+url.PathEscape(code), "GET", "")
	if err != nil {
		return sizeInfo{}, err
	}
	return sizeFromProduct(asObj(data)), nil
}

// sizeFromProduct derives the size fields from a product-detail response.
func sizeFromProduct(d obj) sizeInfo {
	var info sizeInfo
	p := asObj(d["Product"])
	attrs := asObj(d["AdditionalAttributes"])
	if attrs == nil {
		attrs = asObj(p["AdditionalAttributes"])
	}
	info.Storage = cleanStorage(str(attrs["storageinstructions"]))
	if ps := strings.TrimSpace(str(p["PackageSize"])); ps != "" {
		if q, u, ok := parsePackSize(ps); ok {
			info.Size, info.Qty, info.Unit, info.Source = ps, fmtNum(q), u, "package"
			return info
		}
	}
	// Fallback: servings per pack x serving size.
	spp, err1 := strconv.ParseFloat(strings.TrimSpace(str(attrs["servingsperpack-total-nip"])), 64)
	if q, u, ok := parsePackSize(str(attrs["servingsize-total-nip"])); ok && err1 == nil && spp > 0 && u != "each" {
		info.Size = fmtNum(q*spp) + u
		info.Qty, info.Unit, info.Source = fmtNum(q*spp), u, "nutrition"
	}
	return info
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// cleanStorage drops blanks and the retail-floor notes some produce carries ("Stockroom Storage &
// Ambient Display", "Coolroom Storage & Chilled Display"), which say nothing useful about keeping the food at home.
func cleanStorage(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	l := strings.ToLower(s)
	for _, junk := range []string{"stockroom", "ambient display", "coolroom", "chilled display"} {
		if strings.Contains(l, junk) {
			return ""
		}
	}
	return s
}

var (
	sizeSimpleRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(kg|g|ml|l)$`)
	sizeMultiRe  = regexp.MustCompile(`^(\d+)x(\d+(?:\.\d+)?)(kg|g|ml|l)$`)
	sizeCountRe  = regexp.MustCompile(`^(\d+)(?:pk|pack|ea|each)$`)
)

// parsePackSize normalises a Woolies pack size to a number and g / ml / each (kg->g, L->ml).
func parsePackSize(s string) (float64, string, bool) {
	t := strings.ToLower(strings.Join(strings.Fields(s), ""))
	conv := func(n float64, u string) (float64, string) {
		switch u {
		case "kg":
			return n * 1000, "g"
		case "l":
			return n * 1000, "ml"
		}
		return n, u
	}
	switch t {
	case "each", "ea":
		return 1, "each", true
	}
	if m := sizeSimpleRe.FindStringSubmatch(t); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		q, u := conv(n, m[2])
		return q, u, true
	}
	if m := sizeMultiRe.FindStringSubmatch(t); m != nil {
		c, _ := strconv.ParseFloat(m[1], 64)
		n, _ := strconv.ParseFloat(m[2], 64)
		q, u := conv(c*n, m[3])
		return q, u, true
	}
	if m := sizeCountRe.FindStringSubmatch(t); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		return n, "each", true
	}
	return 0, "", false
}

type cupboardResult struct {
	added, skipped, backfilled []any
	migrated                   bool
	backup                     string
}

func sizeCols(i sizeInfo) []string { return []string{i.Size, i.Qty, i.Unit, i.Storage} }

func sizeEntry(code, item string, i sizeInfo) obj {
	e := obj{"stockcode": code, "item": item, "size": i.Size, "size_qty": i.Qty, "size_unit": i.Unit, "storage": i.Storage}
	if i.Source != "" {
		e["sizeSource"] = i.Source
	}
	return e
}

// applyCupboard appends the order's items to the CSV at path, skipping stockcodes already present
// with the same source. A file with the old 5-column header is upgraded in place (after a .bak
// copy) and its rows get sizes back-filled. With dry it computes the result but writes nothing.
func applyCupboard(path string, order obj, today string, dry bool, lookup func(string) sizeInfo) (cupboardResult, error) {
	res := cupboardResult{added: []any{}, skipped: []any{}, backfilled: []any{}}
	source := "order " + str(order["orderId"])

	existing, err := os.ReadFile(path)
	exists := true
	if errors.Is(err, os.ErrNotExist) {
		exists, err = false, nil
	} else if err != nil {
		return res, err
	}
	var recs [][]string
	if exists {
		r := csv.NewReader(bytes.NewReader(existing))
		r.FieldsPerRecord = -1
		if recs, err = r.ReadAll(); err != nil {
			return res, err
		}
	}
	hasHeader := len(recs) > 0 && len(recs[0]) > 0 && recs[0][0] == "stockcode"
	oldFormat := hasHeader && len(recs[0]) <= oldCupboardCols
	if hasHeader && len(recs[0]) > oldCupboardCols && len(recs[0]) != len(cupboardHeader) {
		return res, fmt.Errorf("unrecognised cupboard header %v", recs[0])
	}
	res.migrated = oldFormat

	have := map[string]bool{}
	body := recs
	if hasHeader {
		body = recs[1:]
	}
	for _, r := range body {
		if len(r) >= 4 {
			have[r[0]+"|"+r[3]] = true
		}
	}

	var rows [][]string
	for _, it := range asArr(order["items"]) {
		m := asObj(it)
		if m == nil {
			continue
		}
		code := str(m["stockcode"])
		if have[code+"|"+source] {
			res.skipped = append(res.skipped, obj{"stockcode": code, "item": str(m["name"]), "qty": str(m["qty"]), "source": source})
			continue
		}
		have[code+"|"+source] = true
		info := lookup(code)
		rows = append(rows, append([]string{code, str(m["name"]), str(m["qty"]), source, today}, sizeCols(info)...))
		e := sizeEntry(code, str(m["name"]), info)
		e["qty"], e["source"] = str(m["qty"]), source
		res.added = append(res.added, e)
	}

	// Migration: rebuild existing rows with the size columns.
	var migratedRows [][]string
	if oldFormat {
		for _, r := range body {
			row := append([]string{}, r...)
			for len(row) < oldCupboardCols {
				row = append(row, "")
			}
			info := lookup(row[0])
			migratedRows = append(migratedRows, append(row[:oldCupboardCols], sizeCols(info)...))
			res.backfilled = append(res.backfilled, sizeEntry(row[0], row[1], info))
		}
		res.backup = backupPath(path)
	}
	if dry || (!oldFormat && len(rows) == 0) {
		return res, nil
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	switch {
	case oldFormat:
		w.Write(cupboardHeader)
		w.WriteAll(migratedRows)
		w.WriteAll(rows)
	case !hasHeader && len(body) > 0:
		return res, errors.New("cupboard file has rows but no recognisable header; not touching it")
	case !exists || len(body) == 0 && !hasHeader:
		w.Write(cupboardHeader)
		w.WriteAll(rows)
	default: // already the new format: append
		w.WriteAll(rows)
	}
	if err := w.Error(); err != nil {
		return res, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return res, err
	}
	if oldFormat {
		if err := os.WriteFile(res.backup, existing, 0o644); err != nil {
			return res, err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
			return res, err
		}
		return res, os.Rename(tmp, path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return res, err
	}
	defer f.Close()
	if exists && len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		f.WriteString("\n")
	}
	_, err = f.Write(buf.Bytes())
	return res, err
}

// backupPath returns path+".bak", or a timestamped name if that backup already exists.
func backupPath(path string) string {
	b := path + ".bak"
	if _, err := os.Stat(b); err == nil {
		b = path + ".bak." + time.Now().Format("20060102-150405")
	}
	return b
}
