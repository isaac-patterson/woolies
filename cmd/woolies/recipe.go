package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// File-only recipe commands. Recipes are markdown files (<slug>.md) with optional YAML-ish
// frontmatter. None of these start the browser or take the profile lock.

// ---- parsing ----

type recipe struct {
	Slug        string
	Path        string
	Raw         string
	FrontKeys   []string // frontmatter keys in file order
	Front       map[string]string
	Heading     string // the "# ..." line
	Intro       string // prose between the heading and the first "##"
	Ingredients []string
	Method      []string
	Notes       []string
	Shopping    []string
	CostHead    string // "Cost breakdown (...)" heading text, if present
	CostLines   []string
	HasCost     bool
}

var (
	reNumbered = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	reBullet   = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	reCode     = regexp.MustCompile(`\s*\((\d{3,})\)\s*$`)
	reQty      = regexp.MustCompile(`\s+x(\d+)\s*$`)
	rePrice    = regexp.MustCompile(`\$(\d+(?:\.\d+)?)`)
	reServes   = regexp.MustCompile(`(?i)\bserves\s+(?:about\s+)?(\d+)`)
)

// splitFrontmatter separates a leading "---" block from the body. ok is false when there is none.
func splitFrontmatter(raw string) (front, body string, ok bool) {
	lines := strings.SplitAfter(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return "", raw, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "---" {
			return strings.Join(lines[1:i], ""), strings.Join(lines[i+1:], ""), true
		}
	}
	return "", raw, false
}

func parseRecipe(slug, path, raw string) *recipe {
	r := &recipe{Slug: slug, Path: path, Raw: raw, Front: map[string]string{}}
	front, body, _ := splitFrontmatter(raw)
	for ln := range strings.SplitSeq(front, "\n") {
		k, v, found := strings.Cut(ln, ":")
		k = strings.TrimSpace(k)
		if !found || k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, dup := r.Front[k]; !dup {
			r.FrontKeys = append(r.FrontKeys, k)
		}
		r.Front[k] = v
	}
	section := "intro"
	var intro []string
	for ln := range strings.SplitSeq(body, "\n") {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "## "):
			h := strings.TrimSpace(ln[3:])
			lh := strings.ToLower(h)
			switch {
			case strings.HasPrefix(lh, "ingredients"):
				section = "ingredients"
			case strings.HasPrefix(lh, "method"), strings.HasPrefix(lh, "instructions"), strings.HasPrefix(lh, "directions"):
				section = "method"
			case strings.HasPrefix(lh, "notes"):
				section = "notes"
			case strings.HasPrefix(lh, "shopping list"):
				section = "shopping"
			case strings.HasPrefix(lh, "cost breakdown"):
				section, r.CostHead, r.HasCost = "cost", h, true
			default:
				section = "other"
			}
			continue
		case strings.HasPrefix(ln, "# ") && r.Heading == "" && section == "intro":
			r.Heading = strings.TrimSpace(ln[2:])
			continue
		}
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		switch section {
		case "intro":
			intro = append(intro, t)
		case "ingredients":
			if m := reBullet.FindStringSubmatch(ln); m != nil {
				r.Ingredients = append(r.Ingredients, strings.TrimSpace(m[1]))
			}
		case "method":
			if m := reNumbered.FindStringSubmatch(ln); m != nil {
				r.Method = append(r.Method, strings.TrimSpace(m[1]))
			}
		case "notes":
			if m := reBullet.FindStringSubmatch(ln); m != nil {
				r.Notes = append(r.Notes, strings.TrimSpace(m[1]))
			}
		case "shopping":
			if m := reBullet.FindStringSubmatch(ln); m != nil {
				r.Shopping = append(r.Shopping, strings.TrimSpace(m[1]))
			}
		case "cost":
			r.CostLines = append(r.CostLines, t)
		}
	}
	r.Intro = strings.Join(intro, "\n")
	return r
}

func (r *recipe) title() string {
	if t := r.Front["title"]; t != "" {
		return t
	}
	if i := strings.Index(r.Heading, " ("); i > 0 {
		return r.Heading[:i]
	}
	return r.Heading
}

// num returns v as an int or float64 when it looks numeric, else the string (nil when empty).
func numVal(v string) any {
	if v == "" {
		return nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}

func (r *recipe) serves() any {
	if v := numVal(r.Front["serves"]); v != nil {
		return v
	}
	if m := reServes.FindStringSubmatch(r.Intro); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return nil
}

// shoppingItem is one parsed "Shopping list stockcodes" bullet. Stockcode is nil for lines such as
// "Garlic: have at home" that carry no code.
type shoppingItem struct {
	Text      string
	Name      string
	Stockcode string
	Quantity  int // 0 = not given
	Price     float64
}

func parseShopping(line string) shoppingItem {
	it := shoppingItem{Text: line}
	s := line
	if m := reCode.FindStringSubmatch(s); m != nil {
		it.Stockcode = m[1]
		s = strings.TrimSpace(reCode.ReplaceAllString(s, ""))
	}
	if m := reQty.FindStringSubmatch(s); m != nil {
		it.Quantity, _ = strconv.Atoi(m[1])
		s = strings.TrimSpace(reQty.ReplaceAllString(s, ""))
	}
	if m := rePrice.FindStringSubmatch(s); m != nil {
		it.Price, _ = strconv.ParseFloat(m[1], 64)
	}
	it.Name = s
	return it
}

func (it shoppingItem) obj() obj {
	o := obj{"text": it.Text, "name": it.Name, "stockcode": nil, "quantity": nil}
	if it.Stockcode != "" {
		o["stockcode"] = it.Stockcode
	}
	if it.Quantity > 0 {
		o["quantity"] = it.Quantity
	}
	if it.Price > 0 {
		o["price"] = it.Price
	}
	return o
}

var reCostItem = regexp.MustCompile(`^(.*?)(?:\s+\((.*)\))?:\s*\$(\d+(?:\.\d+)?)$`)
var reCostTotal = regexp.MustCompile(`(?i)^total\s+\$(\d+(?:\.\d+)?)(?:,\s*\$(\d+(?:\.\d+)?)\s+per serving)?`)

func (r *recipe) costObj() any {
	if !r.HasCost {
		return nil
	}
	c := obj{"heading": r.CostHead, "notes": []any{}, "items": []any{}}
	notes, items := []any{}, []any{}
	for _, ln := range r.CostLines {
		if m := reBullet.FindStringSubmatch(ln); m != nil {
			b := strings.TrimSpace(m[1])
			if t := reCostTotal.FindStringSubmatch(b); t != nil {
				c["total"], _ = strconv.ParseFloat(t[1], 64)
				if t[2] != "" {
					c["perServing"], _ = strconv.ParseFloat(t[2], 64)
				}
				continue
			}
			if ci := reCostItem.FindStringSubmatch(b); ci != nil {
				cost, _ := strconv.ParseFloat(ci[3], 64)
				items = append(items, obj{"text": b, "name": ci[1], "detail": ci[2], "cost": cost})
			} else {
				items = append(items, obj{"text": b})
			}
			continue
		}
		notes = append(notes, ln)
	}
	c["notes"], c["items"] = notes, items
	return c
}

func strList(in []string) []any {
	out := []any{}
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func (r *recipe) frontObj() obj {
	o := obj{}
	for _, k := range r.FrontKeys {
		o[k] = numVal(r.Front[k])
		if k == "title" || k == "cost_basis" || k == "cost_calculated" {
			o[k] = r.Front[k] // keep text fields as text (a date or title must not turn numeric)
		}
	}
	return o
}

func (r *recipe) showObj() obj {
	method := []any{}
	for i, s := range r.Method {
		method = append(method, obj{"n": i + 1, "text": s})
	}
	shop := []any{}
	for _, l := range r.Shopping {
		shop = append(shop, parseShopping(l).obj())
	}
	return obj{
		"slug": r.Slug, "file": r.Path, "title": r.title(), "heading": r.Heading,
		"serves": r.serves(), "frontmatter": r.frontObj(), "intro": r.Intro,
		"ingredients": strList(r.Ingredients), "method": method, "notes": strList(r.Notes),
		"shoppingList": shop, "costBreakdown": r.costObj(), "raw": r.Raw,
	}
}

// ---- lookup ----

type recipeFile struct {
	slug, path string
}

func listRecipeFiles(dir string) ([]recipeFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []recipeFile
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || strings.HasPrefix(n, ".") {
			continue
		}
		out = append(out, recipeFile{strings.TrimSuffix(n, ".md"), filepath.Join(dir, n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].slug < out[j].slug })
	return out, nil
}

func loadRecipe(f recipeFile) (*recipe, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	return parseRecipe(f.slug, f.path, string(b)), nil
}

func normWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// resolveRecipe finds one recipe by exact slug, exact title, then by every query word appearing in
// the slug or title. Several matches is an error that lists the candidates.
func resolveRecipe(dir, key string) (*recipe, string, error) {
	files, err := listRecipeFiles(dir)
	if err != nil {
		return nil, "", err
	}
	key = strings.TrimSpace(key)
	words := normWords(key)
	if len(words) == 0 {
		return nil, "", usageErr("recipe name is empty")
	}
	all := map[string]*recipe{}
	for _, f := range files {
		r, err := loadRecipe(f)
		if err != nil {
			return nil, "", err
		}
		all[f.slug] = r
	}
	pick := func(match func(r *recipe) bool, by string) (*recipe, string, error) {
		var hits []*recipe
		for _, f := range files {
			if match(all[f.slug]) {
				hits = append(hits, all[f.slug])
			}
		}
		switch len(hits) {
		case 0:
			return nil, "", nil
		case 1:
			return hits[0], by, nil
		}
		var c []string
		for _, h := range hits {
			c = append(c, fmt.Sprintf("%s (%s)", h.Slug, h.title()))
		}
		return nil, "", fmt.Errorf("%q matches %d recipes, be more specific: %s", key, len(hits), strings.Join(c, "; "))
	}
	steps := []struct {
		by string
		fn func(r *recipe) bool
	}{
		{"slug", func(r *recipe) bool { return r.Slug == key }},
		{"title", func(r *recipe) bool { return strings.EqualFold(r.title(), key) }},
		{"partial", func(r *recipe) bool {
			hay := " " + strings.Join(normWords(r.Slug+" "+r.title()), " ") + " "
			for _, w := range words {
				if !strings.Contains(hay, w) {
					return false
				}
			}
			return true
		}},
	}
	for _, s := range steps {
		r, by, err := pick(s.fn, s.by)
		if err != nil || r != nil {
			return r, by, err
		}
	}
	return nil, "", fmt.Errorf("no recipe matches %q in %s", key, dir)
}

// ---- commands ----

func cmdRecipe(pos []string, o opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("usage: woolies recipe list|show|set|match|cart|save (see `woolies help`)")
	}
	dir := o.str("dir", defaultRecipesDir())
	args := pos[1:]
	switch pos[0] {
	case "list":
		files, err := listRecipeFiles(dir)
		if err != nil {
			return nil, err
		}
		rs := []any{}
		for _, f := range files {
			r, err := loadRecipe(f)
			if err != nil {
				return nil, err
			}
			rs = append(rs, obj{"slug": r.Slug, "title": r.title(), "serves": r.serves(), "cost_per_serving": numVal(r.Front["cost_per_serving"])})
		}
		return obj{"success": true, "dir": dir, "count": len(rs), "recipes": rs}, nil

	case "show":
		if len(args) == 0 {
			return nil, usageErr("usage: woolies recipe show <slug|title words>")
		}
		r, by, err := resolveRecipe(dir, strings.Join(args, " "))
		if err != nil {
			return nil, err
		}
		out := r.showObj()
		out["success"], out["matchedBy"] = true, by
		return out, nil

	case "set":
		return recipeSet(dir, args, o)

	case "match":
		return recipeMatch(dir, args, o)

	case "cart":
		return recipeCart(dir, args, o)

	case "save":
		return recipeSave(dir, args, o)
	}
	return nil, usageErr("unknown recipe subcommand %q. Use list, show, set, match, cart or save", pos[0])
}

// settable frontmatter fields and how to validate them.
var recipeFields = []struct{ flag, key, kind string }{
	{"serves", "serves", "int"},
	{"cost-per-serving", "cost_per_serving", "money"},
	{"total-cost", "total_cost", "money"},
	{"cost-basis", "cost_basis", "text"},
	{"cost-calculated", "cost_calculated", "date"},
	{"title", "title", "text"},
}

var reDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func recipeSet(dir string, args []string, o opts) (obj, error) {
	const usage = "usage: woolies recipe set <slug> [--serves N] [--cost-per-serving 5.86] [--total-cost 23.45] [--cost-basis text] [--cost-calculated YYYY-MM-DD] [--title text] [--dry-run]"
	if len(args) == 0 {
		return nil, usageErr(usage)
	}
	updates := map[string]string{}
	var order []string
	for _, f := range recipeFields {
		v, ok := o[f.flag]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch f.kind {
		case "int":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return nil, usageErr("--%s must be a whole number of at least 1", f.flag)
			}
			v = strconv.Itoa(n)
		case "money":
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n < 0 {
				return nil, usageErr("--%s must be a number, 0 or more", f.flag)
			}
			v = strconv.FormatFloat(n, 'f', 2, 64)
		case "date":
			if !reDate.MatchString(v) {
				return nil, usageErr("--%s must look like 2026-11-07", f.flag)
			}
		}
		if v == "" || strings.ContainsAny(v, "\r\n") {
			return nil, usageErr("--%s needs a single-line value", f.flag)
		}
		updates[f.key] = v
		order = append(order, f.key)
	}
	if len(updates) == 0 {
		return nil, usageErr("nothing to set. %s", usage)
	}
	r, by, err := resolveRecipe(dir, strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	newRaw, changes, notes := applyRecipeSet(r, updates, order)
	dry := o.str("dry-run", "") == "true"
	if !dry && newRaw != r.Raw {
		if err := os.WriteFile(r.Path+".bak", []byte(r.Raw), 0o644); err != nil {
			return nil, err
		}
		tmp := r.Path + ".tmp"
		if err := os.WriteFile(tmp, []byte(newRaw), 0o644); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, r.Path); err != nil {
			return nil, err
		}
	}
	out := obj{"success": true, "slug": r.Slug, "file": r.Path, "matchedBy": by, "dryRun": dry, "changed": newRaw != r.Raw, "changes": changes}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	return out, nil
}

// applyRecipeSet rewrites frontmatter lines (adding missing keys, or a frontmatter block) and the
// matching prose lines. Everything else is kept byte for byte.
func applyRecipeSet(r *recipe, updates map[string]string, order []string) (string, []any, []any) {
	changes, notes := []any{}, []any{}
	front, body, hasFront := splitFrontmatter(r.Raw)
	lines := []string{}
	if hasFront {
		lines = strings.SplitAfter(front, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	for _, key := range order {
		nv := updates[key]
		found := false
		for i, ln := range lines {
			k, v, ok := strings.Cut(ln, ":")
			if !ok || strings.TrimSpace(k) != key {
				continue
			}
			found = true
			old := strings.TrimSpace(v)
			if old != nv {
				nl := "\n"
				if !strings.HasSuffix(ln, "\n") {
					nl = ""
				}
				lines[i] = key + ": " + nv + nl
			}
			changes = append(changes, obj{"field": key, "from": old, "to": nv})
			break
		}
		if !found {
			lines = append(lines, key+": "+nv+"\n")
			changes = append(changes, obj{"field": key, "from": nil, "to": nv})
		}
	}
	for i, ln := range lines { // a last line without a newline would run into the next key
		if i < len(lines)-1 && !strings.HasSuffix(ln, "\n") {
			lines[i] = ln + "\n"
		}
	}
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		lines[len(lines)-1] += "\n"
	}
	newFront := "---\n" + strings.Join(lines, "") + "---\n"
	if hasFront {
		// body is already the rest of the file after the closing ---
	} else {
		body = r.Raw
	}

	// Prose: the "Serves N" sentence in the intro, and the cost breakdown total line.
	if v, ok := updates["serves"]; ok {
		if loc := reServes.FindStringSubmatchIndex(body); loc != nil && introContains(body, loc[0]) {
			old := body[loc[2]:loc[3]]
			body = body[:loc[2]] + v + body[loc[3]:]
			changes = append(changes, obj{"field": "intro prose", "from": old, "to": v})
		} else {
			notes = append(notes, "no \"Serves N\" sentence found in the intro, so only the frontmatter changed")
		}
		if _, hasCost := r.Front["cost_per_serving"]; hasCost {
			if _, redone := updates["cost_per_serving"]; !redone {
				notes = append(notes, "serves changed but cost_per_serving was not recomputed; pass --cost-per-serving if it should change")
			}
		}
	}
	if cps, ok := updates["cost_per_serving"]; ok {
		body = rewriteTotalLine(body, "", cps, &changes)
	}
	if tc, ok := updates["total_cost"]; ok {
		body = rewriteTotalLine(body, tc, "", &changes)
	}
	return newFront + body, changes, notes
}

// introContains reports whether offset is before the first "## " heading.
func introContains(body string, offset int) bool {
	i := strings.Index(body, "\n## ")
	return i < 0 || offset < i
}

var reTotalLine = regexp.MustCompile(`(?m)^(\s*[-*]\s+Total \$)(\d+(?:\.\d+)?)(, \$)(\d+(?:\.\d+)?)( per serving)`)

func rewriteTotalLine(body, total, per string, changes *[]any) string {
	loc := reTotalLine.FindStringSubmatchIndex(body)
	if loc == nil {
		return body
	}
	g := func(n int) string { return body[loc[2*n]:loc[2*n+1]] }
	nt, np := g(2), g(4)
	if total != "" {
		nt = total
	}
	if per != "" {
		np = per
	}
	if nt == g(2) && np == g(4) {
		return body
	}
	*changes = append(*changes, obj{"field": "cost breakdown total line", "from": g(2) + "/" + g(4), "to": nt + "/" + np})
	return body[:loc[0]] + g(1) + nt + g(3) + np + g(5) + body[loc[1]:]
}

// recipeMatch compares a recipe's shopping-list stockcodes with the cupboard.
func recipeMatch(dir string, args []string, o opts) (obj, error) {
	if len(args) == 0 {
		return nil, usageErr("usage: woolies recipe match <slug> [--file cupboard.csv]")
	}
	r, by, err := resolveRecipe(dir, strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	cup, err := loadCupboard(o.str("file", defaultCupboardFile()))
	if err != nil {
		return nil, err
	}
	have := map[string]float64{}
	for _, row := range cup.rows {
		if q, err := strconv.ParseFloat(strings.TrimSpace(row[colQty]), 64); err == nil && row[colStock] != "" {
			have[row[colStock]] += q
		}
	}
	haveL, lowL, missL, otherL := []any{}, []any{}, []any{}, []any{}
	for _, l := range r.Shopping {
		it := parseShopping(l)
		if it.Stockcode == "" {
			otherL = append(otherL, it.Text)
			continue
		}
		need := float64(it.Quantity)
		if need == 0 {
			need = 1
		}
		got := have[it.Stockcode]
		e := it.obj()
		e["need"], e["have"] = need, got
		switch {
		case got >= need:
			haveL = append(haveL, e)
		case got > 0:
			lowL = append(lowL, e)
		default:
			missL = append(missL, e)
		}
	}
	return obj{"success": true, "slug": r.Slug, "matchedBy": by, "cupboardFile": cup.path, "have": haveL, "low": lowL, "missing": missL, "noStockcode": otherL,
		"note": "need defaults to 1 pack when the recipe gives no quantity; cupboard qty is in packs and can be fractional"}, nil
}
