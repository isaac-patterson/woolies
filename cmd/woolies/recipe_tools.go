package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// recipe cart and recipe save. Both are file-only: no browser, no profile lock.

// recipeCart subtracts the cupboard from a recipe's shopping list and builds the add-many input.
// Need is the recipe quantity, or 1 pack when it gives none. The cupboard can hold fractions of a
// pack (0.35 of a bag), so a pack is only skipped when the cupboard covers the whole need.
func recipeCart(dir string, args []string, o opts) (obj, error) {
	if len(args) == 0 {
		return nil, usageErr("usage: woolies recipe cart <slug> [--file cupboard.csv]")
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
	type line struct {
		name string
		need float64
	}
	var order []string
	lines := map[string]*line{}
	skipped := []any{}
	for _, l := range r.Shopping {
		it := parseShopping(l)
		if it.Stockcode == "" {
			skipped = append(skipped, obj{"item": it.Text, "reason": "no stockcode (at home or not yet resolved)"})
			continue
		}
		need := float64(it.Quantity)
		if need == 0 {
			need = 1
		}
		if ln, ok := lines[it.Stockcode]; ok {
			ln.need += need
			continue
		}
		lines[it.Stockcode] = &line{it.Name, need}
		order = append(order, it.Stockcode)
	}
	addMany, buy := []any{}, []any{}
	for _, code := range order {
		ln := lines[code]
		got := have[code]
		if got >= ln.need-1e-9 {
			skipped = append(skipped, obj{"stockcode": code, "item": ln.name, "need": ln.need, "have": got,
				"reason": fmt.Sprintf("cupboard has %g, recipe needs %g", got, ln.need)})
			continue
		}
		qty := int(math.Ceil(ln.need - got - 1e-9))
		e := obj{"stockcode": code, "item": ln.name, "need": ln.need, "have": got, "quantity": qty}
		if got > 0 {
			e["note"] = fmt.Sprintf("cupboard has only %g of the %g needed, so buying %d", got, ln.need, qty)
		}
		buy = append(buy, e)
		n, _ := strconv.Atoi(code)
		addMany = append(addMany, obj{"stockcode": n, "quantity": qty})
	}
	return obj{"success": true, "slug": r.Slug, "matchedBy": by, "cupboardFile": cup.path,
		"addMany": addMany, "buy": buy, "skipped": skipped,
		"note": "Pipe addMany to `woolies add-many -`. add-many SETS quantities, so an item already in the cart is overwritten: check `woolies cart` first."}, nil
}

// ---- recipe save ----

type saveShopping struct {
	Name      string      `json:"name"`
	Stockcode json.Number `json:"stockcode"`
	Quantity  int         `json:"quantity"`
	Price     float64     `json:"price"`  // price of one pack, in dollars
	Share     float64     `json:"share"`  // fraction (0-1] of all the packs bought that the recipe uses
	Detail    string      `json:"detail"` // optional text for the cost line, such as "300g of 700g"
}

type saveInput struct {
	Title       string         `json:"title"`
	Slug        string         `json:"slug"`
	Serves      int            `json:"serves"`
	Intro       string         `json:"intro"`
	Ingredients []string       `json:"ingredients"`
	Method      []string       `json:"method"`
	Notes       []string       `json:"notes"`
	Shopping    []saveShopping `json:"shopping"`
	AtHome      []string       `json:"atHome"`
	CostBasis   string         `json:"costBasis"`
	CostNote    string         `json:"costNote"`
	Date        string         `json:"date"`
}

var reSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(title) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func dollars(cents int) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

// buildRecipe validates the input and renders the markdown. Costs are worked in whole cents: each
// line is price x quantity x share rounded to the cent, and the total is the sum of those rounded
// lines, so the printed lines always add up to the printed total.
func buildRecipe(in saveInput) (md string, slug string, totalCents, perCents int, err error) {
	bad := func(f string, a ...any) (string, string, int, int, error) {
		return "", "", 0, 0, usageErr(f, a...)
	}
	in.Title = strings.TrimSpace(in.Title)
	switch {
	case in.Title == "" || strings.ContainsAny(in.Title, "\r\n"):
		return bad("title is required and must be one line")
	case in.Serves < 1:
		return bad("serves must be a whole number of at least 1")
	case len(in.Ingredients) == 0:
		return bad("ingredients needs at least one entry")
	case len(in.Method) == 0:
		return bad("method needs at least one step")
	}
	for _, group := range [][]string{in.Ingredients, in.Method, in.Notes, in.AtHome} {
		for _, s := range group {
			if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") {
				return bad("ingredients, method, notes and atHome entries must be non-empty single lines")
			}
		}
	}
	slug = in.Slug
	if slug == "" {
		slug = slugify(in.Title)
	}
	if !reSlug.MatchString(slug) {
		return bad("slug %q must be lowercase letters, digits and dashes", slug)
	}
	date := in.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !reDate.MatchString(date) {
		return bad("date must look like 2026-11-07")
	}
	basis := in.CostBasis
	if basis == "" {
		basis = "share of pack used"
	}
	if strings.ContainsAny(basis, "\r\n") {
		return bad("costBasis must be one line")
	}
	var shop, cost []string
	for i, it := range in.Shopping {
		name := strings.TrimSpace(it.Name)
		code, cerr := strconv.Atoi(it.Stockcode.String())
		if name == "" || cerr != nil || code < 100 || it.Price <= 0 {
			return bad("shopping[%d]: needs name, a stockcode (3+ digits) and a price above 0", i)
		}
		qty := max(it.Quantity, 1)
		share := it.Share
		if share == 0 {
			share = 1
		}
		if share < 0 || share > 1 {
			return bad("shopping[%d]: share must be above 0 and at most 1 (the fraction of the packs bought that the recipe uses)", i)
		}
		line := int(math.Round(it.Price * float64(qty) * share * 100))
		totalCents += line
		entry := name
		if qty > 1 {
			entry += " x" + strconv.Itoa(qty)
		}
		shop = append(shop, fmt.Sprintf("%s (%d)", entry, code))
		detail := it.Detail
		if detail == "" {
			switch {
			case share < 1:
				detail = fmt.Sprintf("%g%% of the pack", math.Round(share*1000)/10)
			case qty > 1:
				detail = fmt.Sprintf("%d packs", qty)
			default:
				detail = "1 pack"
			}
		}
		cost = append(cost, fmt.Sprintf("- %s (%s): %s", entry, detail, dollars(line)))
	}
	if len(in.Shopping) > 0 {
		perCents = int(math.Round(float64(totalCents) / float64(in.Serves)))
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\nserves: %d\n", in.Title, in.Serves)
	if len(in.Shopping) > 0 {
		fmt.Fprintf(&b, "cost_per_serving: %s\ntotal_cost: %s\ncost_basis: %s\ncost_calculated: %s\n",
			strings.TrimPrefix(dollars(perCents), "$"), strings.TrimPrefix(dollars(totalCents), "$"), basis, date)
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", in.Title)
	intro := strings.TrimSpace(in.Intro)
	if !reServes.MatchString(intro) {
		intro = strings.TrimSpace(fmt.Sprintf("Serves %d. %s", in.Serves, intro))
	}
	b.WriteString(intro + "\n\n## Ingredients\n")
	for _, s := range in.Ingredients {
		b.WriteString("- " + s + "\n")
	}
	b.WriteString("\n## Method\n")
	for i, s := range in.Method {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if len(in.Notes) > 0 {
		b.WriteString("\n## Notes\n")
		for _, s := range in.Notes {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(in.Shopping) > 0 || len(in.AtHome) > 0 {
		b.WriteString("\n## Shopping list stockcodes\n")
		for _, s := range shop {
			b.WriteString("- " + s + "\n")
		}
		if len(in.AtHome) > 0 {
			b.WriteString("- " + strings.Join(in.AtHome, ", ") + ": have at home\n")
		}
	}
	if len(in.Shopping) > 0 {
		fmt.Fprintf(&b, "\n## Cost breakdown (Woolies, %s)\nServes %d; costed at %d.\n", date, in.Serves, in.Serves)
		if in.CostNote != "" {
			b.WriteString(strings.TrimSpace(in.CostNote) + "\n")
		}
		b.WriteString("\n" + strings.Join(cost, "\n") + "\n")
		fmt.Fprintf(&b, "- Total %s, %s per serving\n", dollars(totalCents), dollars(perCents))
	}
	return b.String(), slug, totalCents, perCents, nil
}

func recipeSave(dir string, args []string, o opts) (obj, error) {
	if len(args) == 0 {
		return nil, usageErr("usage: woolies recipe save <file.json|-> [--force] [--dry-run]")
	}
	var raw []byte
	var err error
	if args[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(args[0])
	}
	if err != nil {
		return nil, usageErr("recipe save: %v", err)
	}
	var in saveInput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, usageErr("recipe save: bad JSON: %v", err)
	}
	md, slug, total, per, err := buildRecipe(in)
	if err != nil {
		return nil, err
	}
	// The result must parse back into the same sections, or the file is not valid.
	parsed := parseRecipe(slug, "", md)
	wantShop := len(in.Shopping)
	if len(in.AtHome) > 0 {
		wantShop++
	}
	if len(parsed.Ingredients) != len(in.Ingredients) || len(parsed.Method) != len(in.Method) || len(parsed.Shopping) != wantShop {
		return nil, fmt.Errorf("internal check failed: the rendered recipe does not parse back into its sections")
	}
	path := filepath.Join(dir, slug+".md")
	dry := o.str("dry-run", "") == "true"
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if exists && o.str("force", "") != "true" && !dry {
		return nil, usageErr("%s already exists; pass --force to replace it (the old file is kept as .bak)", path)
	}
	out := obj{"success": true, "slug": slug, "file": path, "dryRun": dry, "exists": exists}
	if len(in.Shopping) > 0 {
		out["totalCost"] = float64(total) / 100
		out["costPerServing"] = float64(per) / 100
	}
	if dry {
		out["markdown"] = md
		return out, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if exists {
		old, _ := os.ReadFile(path)
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return nil, err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(md), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return out, nil
}
