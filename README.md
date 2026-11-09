# woolies

A CLI for Woolies: search products, read and edit the cart, read past orders. Output is JSON on stdout.

It drives a real browser (Brave by default) through chromedp, with a persistent profile. That keeps the Woolies login between calls, and it makes requests from inside the page so the site sees a normal session. It works from any folder and needs no MCP session.

## Build and install

Needs Go and a Chromium-based browser (Brave by default; set `browser_path` for Chrome or another one).

```bash
git clone <this repo> woolies && cd woolies
make install          # builds bin/woolies and symlinks it into $(brew --prefix)/bin (override with PREFIX=...)
mkdir -p ~/.woolies && cp config.example.json ~/.woolies/config.json   # then edit it
woolies login
```

See Development below for the other `make` targets.

chromedp is pinned to v0.14.2 (with its matching `cdproto` and `go-json-experiment`). The newer v0.23 has a different API.

## Claude Code skills

`.claude/skills/` has three [Claude Code](https://code.claude.com) skills that drive this CLI. Type the skill name (for example `/groceries`) or just ask in plain words; Claude picks the right skill from what you say.

### `/groceries`

Finds products and fills the cart, always confirming in chat before it changes anything. It also keeps the cupboard list up to date and can plan meals around this week's specials.

- "Add butter, eggs and a loaf of bread to my cart."
- "What's in my cart right now, and am I over the minimum spend?"
- "What was in my last order?"
- "I've picked up my order, add it to the cupboard."
- "I bought milk somewhere else, and I've run out of rice."
- "Plan three cheap dinners from what's on special this week."

### `/add-recipe`

Finds and saves recipes from the internet, works out costs and the shopping list, then hands the list to `/groceries`.

- "Find me something cheesy with chicken for four people."
- "Save a cheap vegetarian pasta bake recipe."
- "I want to cook the lemon chicken and the pasta bake this week. What do I need to buy?"
- "How much does the pasta bake cost per serving now?"
- "Change the pasta bake to serve six."

### `/sous-chef`

Talks you through cooking a saved recipe one step at a time and keeps the cupboard up to date as you use things.

- "Let's cook the pasta bake."
- "Sous chef, I'm making dinner. What can I make with what's in the cupboard?"
- "Do I have everything for the lemon chicken?"
- "Next." / "Done, what now?"
- "The sauce is too runny, help."
- "I used half the mozzarella."

They load automatically in sessions started in this repo. To use them from another folder, symlink each one into that folder's `.claude/skills/` (Claude Code follows symlinked skill folders):

```bash
mkdir -p ~/somewhere/.claude/skills
for s in groceries add-recipe sous-chef; do ln -s "$PWD/.claude/skills/$s" ~/somewhere/.claude/skills/$s; done
```

The skills hold no personal details. They read yours from a `CLAUDE.md` in the same folder as your `cupboard_file`: where you live, your store and its ID, the minimum spend, food preferences, and an "Always at home" list of things never to buy.

## Commands

```
woolies status                      logged in? store id, reserved slot, minimum spend, cart summary
woolies login                       visible browser for manual sign-in (close the window when done; it then verifies the login)
woolies logout                      sign out (clears the profile's cookies and site data)
woolies search <term...> [--page-size 8] [--sort TraderRelevance|PriceAsc|PriceDesc|Name] [--special] [--page 1] [--nutrition]   --nutrition adds per-100g nutrition to each product (one extra request per product; keep --page-size small)
woolies product <stockcode>
woolies specials (--term <t> | --category <nodeId>) [--page-size 8] [--page 1]
woolies specials --all [--food] [--department <a,b>] [--min-discount <percent>] [--limit 200] [--max-pages 60] [--refresh] [--max-age 24]   every current deal, biggest discount first (cached)
woolies categories
woolies store [--postcode <postcode>]
woolies config                      effective settings and where each came from
woolies addresses                   saved delivery addresses (ids for --address)
woolies timeslots [--method Courier|Pickup] [--address <addressId>] [--all]   delivery windows (available only unless --all); read-only, never reserves
woolies cart [--summary] [--compact]   --summary: total, savings, item count, shortfall to the minimum spend and to each boosted offer
woolies add <stockcode> [quantity=1]   puts the item in the cart AT that quantity (sets, does not increment)
woolies add-many <file|->           JSON [{"stockcode":123,"quantity":2},...]
woolies set-qty <stockcode> <quantity>
woolies remove <stockcode>
woolies clear [--dry-run]           empty the whole cart (--dry-run only lists what would be removed)
woolies quick-add <term...> [qty=1] [--dry-run]   search, set the top available Woolies-stocked result in the cart, report what was picked
woolies orders [--order-id N] [--limit 5]   read-only
woolies orders --latest [--active]           newest order with its items in one call (read-only). --active skips cancelled orders (also on the list); cancelled orders carry `cancelled` and `replacedBy`
woolies offers [--all] [--cart [--reach 30]]   --cart checks offers against the cart (qualifies / within reach / estimated points); active Everyday Rewards points offers (read-only; never boosts). Only Woolworths shopping offers by default; --all adds partner promos
woolies activate <offer id>... [--yes]   boost (activate) offers so purchases earn the points; without --yes it only shows what it would boost
woolies activate --all [--yes]               every unboosted Woolworths shopping offer
woolies cupboard add-latest [--file P] [--dry-run]   append newest order's items to a cupboard CSV, with pack size and per-100g nutrition (kj, protein, fat, sat fat, carbs, sugars, fibre, sodium) from each product page (skips rows already present for that order; default: `cupboard_file`)
woolies cupboard list|add|remove|use|set-qty|clear ...   manage the cupboard CSV by hand (file-only, no browser or lock; writes are atomic, keep a .bak, and take --dry-run; `clear` needs --yes). See `woolies help`
woolies recipe list [--dir D]               slug, title, serves, cost_per_serving (default: `recipes_dir`)
woolies recipe show <slug|title words>     parsed JSON (frontmatter, ingredients, method, notes, shoppingList, costBreakdown) plus raw markdown; partial matches, ambiguous = error listing candidates
woolies recipe set <slug> [--serves N] [--cost-per-serving X] [--total-cost X] [--cost-basis T] [--cost-calculated YYYY-MM-DD] [--title T] [--dry-run]   edit frontmatter and the matching "Serves N" / "Total" prose lines; atomic, keeps <slug>.md.bak
woolies recipe match <slug> [--file cupboard.csv]   shopping-list stockcodes vs the cupboard: have / low / missing / noStockcode (file-only)
woolies recipe cart <slug> [--file cupboard.csv]    shopping list minus the cupboard, as `add-many` JSON (`addMany`) plus `buy` and `skipped` with reasons (file-only)
woolies recipe save <file.json|-> [--force] [--dry-run]   write a validated recipe file and work out total_cost / cost_per_serving from pack price x quantity x share (file-only)
```

Exit codes: 0 success, 1 failure (JSON error on stderr), 2 bad usage.

## Development

The Makefile wraps the usual Go commands. Run `make help` to list the targets. It is based on Alex Edwards' [time-saving Makefile for Go projects](https://www.alexedwards.net/blog/a-time-saving-makefile-for-your-go-projects).

| Command | What it does |
|---|---|
| `make build` | Builds `bin/woolies`. |
| `make install` | Builds, then symlinks `$(brew --prefix)/bin/woolies` (or `/usr/local/bin`) to `bin/woolies`. Set `PREFIX=...` to put it elsewhere. |
| `make test` | Runs the tests with the race detector. They run offline and never open a browser. |
| `make test/cover` | Runs the tests and opens an HTML coverage report in your browser. |
| `make audit` | Runs the tests, then `go mod tidy -diff`, `go mod verify`, a gofmt check, `go vet`, staticcheck and govulncheck. The last two run through `go run ...@latest`, so they need a network connection. |
| `make tidy` | Tidies `go.mod`, runs `go fix` (which updates code to newer Go idioms) and `go fmt`. It rewrites files, so review the diff. |
| `make upgradeable` | Lists direct dependencies that have newer versions. Read-only. Keep chromedp at v0.14.2 even though it shows an upgrade. |
| `make clean` | Deletes `bin/woolies`. |
| `make push` | Asks for confirmation, runs `make audit`, checks the working tree is clean, then runs `git push`. |

Before committing a change, run `make audit`; it must pass.

## How the session works

- The login lives in the browser profile on disk, `~/.woolies/profile`. Any call that launches the browser with that profile is logged in. The cart lives on Woolies' servers, tied to the account.
- Each call launches a headless browser, runs, and closes it (about 3 seconds). Batch work with multiple search terms and `add-many` instead of looping.
- Only one browser can use the profile at a time. A lock file at `~/.woolies/lock` makes concurrent calls queue instead of failing.
- When the login expires, run `woolies login`, sign in by hand, and close the window.
- Cookies are never printed.
- `timeslots` only lists windows; there is no command to reserve one. Pickup windows come back empty from this endpoint, so use `--method Courier` (the default).
- Cart writes (`add`, `add-many`, `set-qty`, `remove`, `clear`, `quick-add`) set absolute quantities, as the Woolies API does, and return the cart re-read afterwards.

## Compact output, variable weights and order status

- `--compact` on `search`, `specials` and `cart` replaces the product arrays with one-line strings under `lines` (`stockcode | name | size | price | cup string | flags | xqty`). A leading `~` on the price means it is an estimate. Set `"compact": true` in the config (or `WOOLIES_COMPACT=true`) to make it the default; `--no-compact` gives the full JSON for one call, and `--nutrition` always does. One-line output drops fields such as nutrition, departments and `multibuy`, so scripts that read those fields should leave the setting off or pass `--no-compact`.
- `isVariableWeight` and `priceIsEstimate` are set when a product's name or pack size has a weight range (such as `280g - 800g`) or says "approx". The shelf price is then only an estimate; the real charge depends on the weight picked. This is a text heuristic, as the site gives no flag for it.
- `specials --all --match <regex>` filters by name (case-insensitive).
- `cart --summary` adds a `summary` with the total, savings (the site's figure if the totals have one, else worked out from `wasPrice`), item count, shortfall to the minimum spend, and the shortfall to each boosted offer's minimum spend.
- `offers --cart` adds `cartCheck`: offers the cart `qualifies` for, those `withinReach` (gap in dollars, `--reach` sets how far, 30 by default), `farAway`, in-store-only ones, `warnings`, and `estimatedPoints`. The estimate counts boosted offers only, takes the best multiplier (it includes the base points) plus all fixed offers, and treats product offers as needing that product's spend. It is an estimate, not the checkout figure.
- A cancelled order has `cancelled: true`. Amending a placed order cancels it and creates a new order id, so a cancelled order whose pickup window matches a newer active one gets `replacedBy`. `orders --active` (list or `--latest`) skips cancelled ones, and the list result has the newest listed order's pickup `window` at the top level.

## Listing every deal

`woolies specials --all` pages through the lists under the site's Specials category, one request at a time with a short pause. That is about 110 requests and takes around 2 minutes, and other `woolies` calls wait for the lock meanwhile (they give up after 2 minutes). The result is cached, so later calls don't open a browser and can filter differently.

- **Cache:** `~/.woolies/specials-cache.json` holds the full unfiltered deal list. It's used for up to `--max-age` hours (24 by default, 0 to always fetch) and never past the next Wednesday midnight, when weekly specials change. `--refresh` fetches now, and `--max-pages` also forces a fetch. Runs with errors or truncated lists aren't saved, and `woolies logout` deletes the file because it can include personalised offers.
- **Cache limits:** a cached price or availability can be out of date, so re-check before buying. The cache isn't tied to a store: `storeId` in the output shows which store it was fetched for, so use `--refresh` after changing store.

- **Lists read:** My Specials and Offers, Half Price, Lower Shelf Price, Seasonal Price and Buy More Save More. Everyday Low Price and Bundles are skipped because they aren't discounts.
- **Left out:** products with no calculable discount, unavailable products, and third-party marketplace listings.
- **Each product adds:** `dealType`, `discountPercent`, `departments`, `categories`, and `multibuy` when the product has a multibuy offer.
- **`dealType`:** `half-price`, `special`, `price-drop` (`wasPrice` and `wasDate` come from the site's tag) or `multibuy` (the discount only applies when you buy `multibuy.quantity`, and it's measured against the current price).
- **`--food`:** drops a product if any of its departments is non-food, including alcohol and Health & Wellness. It's a heuristic, as the site tags departments loosely.
- **`--department`:** comma-separated, case-insensitive partial names, such as `meat,freezer`.
- **`--limit`:** 200 by default, 0 for no limit. The cut comes after sorting, so a low limit is mostly half-price items.
- **Result fields:** `cache` (`hit`, `fetchedAt`, and `ageMinutes` or `saved`), `storeId`, `totalDeals`, `matched`, `limited`, a per-list `lists` report (`truncated` if `--max-pages` was hit), and `incomplete` with `errors` if a page failed partway.

## Everyday Rewards offers

`woolies offers` lists the account's active Everyday Rewards points offers (the Boost offers), read-only. `woolies activate` boosts them.

- **How it reads them:** the offers come from `prod.api-wr.com/wx-rtl-offer-v2/offers`, a different host from the shop that takes the page's own auth headers. So the command loads the home page and reads the offers response the page already fetches, as `orders` does, instead of calling the API itself.
- **Each offer has:** `id`, `boosted`, `section` (such as `WEEKLY_BOOSTERS`), `heading`, `description`, `startsAt`, `endsAt`, `reward` (`type` and `value`), `minimumSpend` in dollars, `channels`, `bannerIds`, and `primaryProductId` when the offer is for one product. The long terms text is left out.
- **Partner promos:** by default only Woolworths shopping offers are shown. Offers with other banner ids (home loans, banking, BIG W, health brands, gift cards) are hidden and counted in `hidden`; `--all` includes them. The Woolworths banner ids (`1005`, `1030`) were worked out from which offers carry them, so check `woolworthsBanners` in `offers.go` if offers go missing.
- **Result fields:** `total` and `boosted` count the shown offers, and `hidden` counts the partner promos left out.
- **Activating:** `woolies activate <id>...` takes an offer `id` (or its `couponId`) from `offers`. Boosting changes the account and can't be undone, so nothing is sent unless `--yes` is passed; without it the command lists what it would boost. `--all` means every unboosted Woolworths offer. Already-boosted and button-less offers (such as "Discover" links) are skipped and listed, and a failed boost is reported per offer with the API's reason.
- **How it boosts:** it sends `POST prod.api-wr.com/wx/v1/csl/customers/offers/boost` from inside the page with body `{"offerIds":[<couponId>...]}`, as the site does. The `hashcrn` header is the response of `/api/v3/ui/shopper/rewards/crn/hash` and `client_id` is read from the request the Rewards page (`/shop/myaccount/woolworthsrewards`) makes to the rewards service. It isn't the same value as the offers list uses, and nothing is stored in the repo.

## Configuration

Settings live in a JSON file, `~/.woolies/config.json` (or the path in `WOOLIES_CONFIG`). Copy `config.example.json` to start. Every key is optional, a leading `~/` is expanded, and an unknown key is an error so typos show up. Each key can be overridden by an environment variable, and a command-line flag (`--postcode`, `--file`, `--dir`) wins over both. `woolies config` prints what is in effect and where each value came from.

| Key | Env variable | Default | Used for |
|---|---|---|---|
| `browser_path` | `WOOLIES_BROWSER_PATH` | `/Applications/Brave Browser.app/Contents/MacOS/Brave Browser` | the browser chromedp drives |
| `profile_dir` | `WOOLIES_PROFILE_DIR` | `~/.woolies/profile` | the browser profile that holds the login |
| `postcode` | `WOOLIES_POSTCODE` | none | `store`, when the account has no address |
| `cupboard_file` | `WOOLIES_CUPBOARD_FILE` | `~/.woolies/cupboard.csv` | the `cupboard` commands and `recipe match` |
| `recipes_dir` | `WOOLIES_RECIPES_DIR` | `~/.woolies/recipes` | the `recipe` commands |
| `compact` | `WOOLIES_COMPACT` | `false` | `true` makes `search`, `specials` and `cart` print one line per item by default (`--no-compact` turns it off for a call; `--nutrition` always gets full JSON) |

`WOOLIES_HOME` (default `~/.woolies`) moves the data folder: the config file, profile, lock file and specials cache. `WOOLIES_DEBUG=1` prints timings to stderr.

## Layout

- `cmd/woolies/main.go`: argument parsing and dispatch.
- `cmd/woolies/config.go`: the config file, env overrides and the `config` command.
- `cmd/woolies/commands.go`: one function per command.
- `cmd/woolies/specials.go`: `specials --all`: the paging, deal calculation and filters.
- `cmd/woolies/recipe.go`: the file-only `recipe` commands (markdown parser, fuzzy lookup, `set`, `match`). Tests use copies of real recipes in `cmd/woolies/testdata/recipes/`.
- `cmd/woolies/recipe_tools.go`: `recipe cart` and `recipe save`.
- `cmd/woolies/insights.go`: `--compact` lines, `cart --summary` and the `offers --cart` check.
- `cmd/woolies/specials_cache.go`: the `specials --all` cache.
- `cmd/woolies/offers.go`: the `offers` command: captures the page's offers response and trims it.
- `cmd/woolies/activate.go`: the `activate` command: resolves ids against the offers list and sends the boost call.
- Tests (`*_test.go`) run offline. `fakesite_test.go` stands in for the Woolies API (it swaps `request`, `captureOrderBodies`, `captureOfferBodies` and `postBoost`), so commands are tested without a browser or an account; `make test/cover` shows coverage (about 86%; the rest is the code that drives the browser itself).
- `cmd/woolies/browser.go`: lock, browser launch, and the in-page `fetch` helper.
- `cmd/woolies/trim.go`: trims the site's JSON down to the fields that matter.

## Notes

- The `/api` endpoints are the website's own and are undocumented, so they can change without notice.
- `--page-size` and `specials` are trimmed client-side, because the API can return more than asked for and `isSpecial` is not always honoured.
- The site rejects page sizes over 36, so `--page-size` is capped there and the result carries a `warning`. Use `--page` for more.
- Never check out or pay with this tool. That stays a manual step on the website.

## Licence

MIT, see `LICENSE`. Not affiliated with or endorsed by Woolworths Group.
