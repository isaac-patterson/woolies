// Command woolies is a CLI for Woolies: search, cart and past orders,
// driven through a persistent-profile browser. Output is JSON on stdout.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func help() string {
	return `woolies - Woolies CLI (JSON on stdout, errors on stderr, exit 1 on failure, 2 on bad usage)

  woolies status                      logged in? store id, reserved slot, minimum spend, cart summary
  woolies login                       visible browser for manual sign-in (close the window when done; it then verifies the login)
  woolies logout                      sign out (clears the profile's cookies and site data)
  woolies search <term...> [--page-size 8] [--sort TraderRelevance|PriceAsc|PriceDesc|Name] [--special] [--page 1]
  woolies product <stockcode>
  woolies specials (--term <t> | --category <nodeId>) [--page-size 8] [--page 1]
  woolies specials --all [--food] [--department <a,b>] [--min-discount <percent>] [--limit 200] [--max-pages 60] [--refresh] [--max-age 24]
                                      every current deal, biggest discount first; --limit 0 for no limit. Cached for --max-age hours and until the
                                      next Wednesday; a fetch takes around 2 minutes, a cached call is instant. --refresh fetches now
  woolies categories
  woolies store [--postcode <postcode>]   store details (postcode: flag, else the account address, else the config)
  woolies config                      effective settings, where each came from, and the config file path
  woolies addresses                   saved delivery addresses (ids for --address)
  woolies timeslots [--method Courier|Pickup] [--address <addressId>] [--all]   delivery windows (available only unless --all); read-only, never reserves
  woolies cart
  woolies add <stockcode> [quantity=1]   puts the item in the cart AT that quantity (sets, does not increment)
  woolies add-many <file|->           JSON [{"stockcode":123,"quantity":2},...] (quantities are set, not incremented)
  woolies set-qty <stockcode> <quantity>
  woolies remove <stockcode>
  woolies clear [--dry-run]           empty the whole cart (--dry-run only lists what would be removed)
  woolies quick-add <term...> [qty=1] [--dry-run]   search, set the top available Woolies-stocked result in the cart, report what was picked
  woolies orders [--order-id N] [--limit 5]   read-only
  woolies orders --latest             newest order with its items in one call (header + items); picked by date, never filtered by status.
                                      The status field is whatever the site reports and can lag the Woolies app
  woolies offers [--unboosted] [--section WEEKLY_BOOSTERS] [--search <text>]
                                      active Everyday Rewards points offers, with boosted true/false, dates, minimum spend and reward.
                                      Read-only: it reads the offers the home page loads and never boosts anything
  woolies cupboard list [--file P] [--search text]      all rows (item substring filter), with total
  woolies cupboard add <item...> [--qty N] [--source had|elsewhere|..] [--stockcode N] [--size 500g]
                                      add by hand (default source had, confirmed today); if the stockcode (else item name) is already
                                      there its qty is increased instead of duplicating
  woolies cupboard remove <item|stockcode>              delete a row (exact stockcode, else item name; several matches: lists them, deletes nothing)
  woolies cupboard use <item|stockcode> [--qty 1]       reduce qty; the row is deleted when it reaches 0
  woolies cupboard set-qty <item|stockcode> <N>         set qty exactly (0 removes the row)
  woolies cupboard clear [--dry-run] [--yes]            empty the cupboard keeping the header; needs --yes (or --dry-run to preview)
                                      All of these take --file P (default: cupboard_file in the config). Writes accept --dry-run, are
                                      atomic (temp file + rename) and keep the previous file as <file>.bak. File-only: no browser, no lock.
  woolies recipe list [--dir D]                         slug, title, serves, cost_per_serving for each recipe (default: recipes_dir in the config)
  woolies recipe show <slug|title words>               parsed recipe as JSON (frontmatter, ingredients, method steps, notes, shoppingList with
                                      stockcode/quantity, costBreakdown) plus the raw markdown. Partial slug or title words match;
                                      several matches: error listing the candidates
  woolies recipe set <slug> [--serves N] [--cost-per-serving 5.86] [--total-cost 23.45] [--cost-basis text] [--cost-calculated YYYY-MM-DD] [--title text]
                                      update frontmatter (and the intro "Serves N" and cost "Total" prose lines); all else kept as is.
                                      Atomic, keeps <slug>.md.bak, takes --dry-run. Costs are not recomputed when serves changes
  woolies recipe match <slug> [--file cupboard.csv]     shopping-list stockcodes vs the cupboard: have / low / missing / noStockcode
                                      (need = the recipe quantity or 1 pack; file-only, no browser or lock; all recipe commands take --dir)
  woolies cupboard add-latest [--file <path>] [--dry-run]
                                      append the newest order's items to a cupboard CSV with columns
                                      stockcode,item,qty,source,confirmed,size,size_qty,size_unit,storage. source is "order <id>",
                                      confirmed is today. Sizes come from each product's page (g/ml/each, kg->g and L->ml; blank when
                                      unknown, never guessed). Skips stockcodes already present with the same source, so re-running is
                                      safe; creates the file with a header if missing. An old 5-column file is upgraded in place
                                      (copied to <file>.bak first) and its rows get sizes back-filled. Default file
                                      cupboard_file from the config. --dry-run reports adds and back-fills without writing or backing up.
                                      Same newest-order logic as orders --latest

Config: JSON at ~/.woolies/config.json (or $WOOLIES_CONFIG) with keys browser_path, profile_dir, postcode, cupboard_file,
recipes_dir. Each can be overridden by an env var (WOOLIES_BROWSER_PATH, WOOLIES_PROFILE_DIR, WOOLIES_POSTCODE, WOOLIES_CUPBOARD_FILE,
WOOLIES_RECIPES_DIR); flags win over both. WOOLIES_HOME moves ~/.woolies. Run "woolies config" to see what is in effect.
Calls launch a headless browser and are serialized; batch terms/items rather than looping.
`
}

var booleanFlags = map[string]bool{"special": true, "help": true, "dry-run": true, "all": true, "food": true, "refresh": true, "latest": true, "yes": true, "unboosted": true}

func parseArgs(args []string) ([]string, opts, error) {
	var pos []string
	o := opts{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		key, val, hasVal := strings.Cut(a[2:], "=")
		switch {
		case booleanFlags[key]:
			o[key] = "true"
		case hasVal:
			o[key] = val
		case i+1 < len(args):
			i++
			o[key] = args[i]
		default:
			return nil, nil, usageErr("--%s needs a value", key)
		}
	}
	return pos, o, nil
}

var commands = map[string]func([]string, opts) (obj, error){
	"status":     cmdStatus,
	"search":     cmdSearch,
	"product":    cmdProduct,
	"specials":   cmdSpecials,
	"categories": cmdCategories,
	"store":      cmdStore,
	"cart":       cmdCart,
	"add":        cmdAdd,
	"add-many":   cmdAddMany,
	"set-qty":    cmdSetQty,
	"remove":     cmdRemove,
	"addresses":  cmdAddresses,
	"timeslots":  cmdTimeslots,
	"clear":      cmdClear,
	"quick-add":  cmdQuickAdd,
	"orders":     cmdOrders,
	"offers":     cmdOffers,
	"cupboard":   cmdCupboard,
	"recipe":     cmdRecipe,
	"logout":     cmdLogout,
	"config":     cmdConfig,
}

func printJSON(f *os.File, v any) {
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func run() int {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(help())
		return 1
	}
	cmd := args[0]
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		fmt.Print(help())
		return 0
	}
	pos, o, err := parseArgs(args[1:])
	if err == nil && o["help"] == "true" {
		fmt.Print(help())
		return 0
	}
	_, known := commands[cmd]
	if err == nil && cmd != "login" && !known {
		err = usageErr("unknown command %q. Run `woolies help`.", cmd)
	}
	if err != nil {
		printJSON(os.Stderr, obj{"success": false, "error": err.Error()})
		return 2
	}

	if _, cerr := loadConfig(); cerr != nil {
		printJSON(os.Stderr, obj{"success": false, "error": cerr.Error()})
		return 1
	}

	if cmd == "config" || cmd == "recipe" || cmd == "cupboard" && cupboardFileOnly(pos) {
		// File-only: no browser, no profile lock.
		result, err := commands[cmd](pos, o)
		if err != nil {
			printJSON(os.Stderr, obj{"success": false, "error": err.Error()})
			if _, ok := errors.AsType[*usageError](err); ok {
				return 2
			}
			return 1
		}
		printJSON(os.Stdout, result)
		return 0
	}

	if err := acquireLock(2 * time.Minute); err != nil {
		printJSON(os.Stderr, obj{"success": false, "error": err.Error()})
		return 1
	}
	defer releaseLock()
	defer closeBrowser()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		closeBrowser()
		releaseLock()
		os.Exit(130)
	}()

	var result obj
	if cmd == "login" {
		result, err = cmdLogin()
	} else {
		result, err = commands[cmd](pos, o)
	}
	if err != nil {
		printJSON(os.Stderr, obj{"success": false, "error": err.Error()})
		if _, ok := errors.AsType[*usageError](err); ok {
			return 2
		}
		return 1
	}
	printJSON(os.Stdout, result)
	if result["success"] == false {
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
