---
name: groceries
description: Woolies shopping via the `woolies` CLI. Use for anything about the user's cart or orders: add or remove items ("add soap and butter"), check the cart, see past orders, or take a ready-made ingredient list (e.g. from /add-recipe) and fill the cart.
---

# /groceries

Finds products and fills the user's Woolies cart using the `woolies` CLI (this repo; browser profile at `~/.woolies/profile`). The skill lives in the woolies repo's `.claude/skills/` and can be symlinked into other projects' `.claude/skills/`, so use absolute paths for context files.

Context: run `woolies config` to find the user's files. Their grocery notes are the `CLAUDE.md` in the same folder as `cupboard_file` (location, store, minimum spend, preferences, "Always at home"). Read them first if they exist. Recipes live in `recipes_dir` and are handled by the `/add-recipe` skill (read through `woolies recipe list|show|match`, file-only); this skill only receives their ingredient list.

## woolies CLI rules
All output is JSON on stdout; errors are JSON on stderr with exit 1. Run it via Bash.
- Every call launches a browser and calls are serialized (one process may use the profile). NEVER run woolies calls in parallel, and never in background alongside another woolies call.
- For macro or nutrition questions ("high-protein snacks", "lowest sugar yoghurt"), add `--nutrition` to `woolies search` with a small `--page-size` (about 5-8): each product gets per-100g `nutrition` (`kj`, `protein_g`, `fat_g`, `sat_fat_g`, `carb_g`, `sugars_g`, `fibre_g`, `sodium_mg`, only those Woolies lists). `"nutrition": null` means none listed (common for fresh produce), and `nutritionError` means the lookup failed. Compare per 100g, never guess missing values, and say so when you skip a product for that reason. It costs one extra request per product, so don't use it for ordinary shopping. Per-100g is for the product as sold: cooked weights differ.
- Batch to keep the number of calls low: several search terms in one `woolies search`, and one `woolies add-many` for all additions.
- Before the first call in a session, run `woolies status`. If `loggedIn` is false, or any call errors about login, tell the user to run `woolies login` themselves (it opens a visible browser), then retry once they say it is done (`woolies login` verifies the sign-in itself and returns `{success, loggedIn}`; `success: false` means it did not stick, so ask them to try again). Do not try to log in yourself. `woolies logout` signs them out; never run it unless they ask.
- Never print or store credentials or cookies.

Commands:
```
woolies status                      # {loggedIn, storeId}
woolies search <term...> [--page-size 8] [--sort TraderRelevance|PriceAsc|PriceDesc|Name] [--special] [--page 1] [--nutrition]
    # one term -> {success, searchTerm, totalResults, products:[{stockcode,displayName,packageSize,price,cupString,isOnSpecial,wasPrice?,isAvailable,isMarketProduct}]}
    # several terms -> {success, results:[that object per term]}
woolies product <stockcode>
woolies specials (--term <t> | --category <nodeId>) [--page-size 8] [--page 1]
woolies specials --all [--food] [--department <a,b>] [--min-discount <percent>] [--limit 200] [--max-pages 60] [--refresh] [--max-age 24]
    # every current deal, biggest discount first -> {success, cache:{hit,fetchedAt,ageMinutes?,saved?}, storeId, totalDeals, matched, limited?, incomplete?, errors?, lists, products:[{...search fields, dealType, discountPercent, departments, categories, multibuy?:{quantity,price}, wasDate?}]}
    # dealType: half-price | special | price-drop | multibuy. --department takes partial names, e.g. meat,freezer. --limit 0 for no limit
    # cached for 24 hours and until the next Wednesday: about 2 minutes when it fetches, instant from the cache. --refresh fetches now
# --page-size is capped at 36 (the site's limit); the result then has a `warning`. Use --page for more.
woolies categories
woolies store [--postcode <postcode>]   # store details; the postcode defaults to the account address, then the config
woolies config                      # the user's settings (cupboard_file, recipes_dir, postcode, ...)
woolies cart                        # {success, cart:{items:[{stockcode,name,qty,unitPrice,...}], totals, totalQuantity, errors}}
woolies add <stockcode> [quantity=1]   # sets the item to that quantity in the cart; it does NOT increment
woolies add-many <file|->           # JSON array [{"stockcode":123,"quantity":2},...]; use: woolies add-many - <<'EOF' ... EOF
woolies set-qty <stockcode> <quantity>
woolies remove <stockcode>
woolies clear [--dry-run]           # empties the whole cart; always run --dry-run first, and only clear for real after the user's yes in chat
woolies quick-add <term...> [qty=1] [--dry-run]   # picks the top available Woolies-stocked search result and sets it in the cart; prefer search + add-many, use this only when the user asks for it, and --dry-run first
woolies addresses                   # saved addresses (read-only)
woolies timeslots [--method Courier|Pickup] [--address <addressId>] [--all]   # delivery windows (read-only; never reserves). Pickup returns none, so it is only useful for Courier
woolies orders [--order-id N] [--limit 5]   # read-only past orders
woolies logout                      # signs the user out (clears the profile); only when they ask
woolies orders --latest             # newest order with its items in one call (picked by date, not filtered by status)
woolies offers [--all]   # read-only Everyday Rewards points offers; boosting is not supported, tell the user to boost in the app. Shows only Woolworths shopping offers unless --all
woolies cupboard list|add|remove|use|set-qty|clear   # local cupboard file, no browser, whole-number qty; see Flow step 1
woolies cupboard add-latest [--dry-run]   # append the newest order's items to the cupboard file (dedupes by stockcode + order); the only cupboard command that calls Woolies
```

## Flow
Input is either a list of items the user names ("soap, butter") or an ingredient list handed over by `/add-recipe`.
1. **Check state.** `woolies status`, then `woolies cart` and, if relevant, `woolies orders` so you don't rebuy what is already in the cart or was just ordered. Skip items in "Always at home" unless the user explicitly asks. Also check the cupboard with `woolies cupboard list` (`--search <word>` for one item) and skip or flag anything already there ("you have butter in the cupboard, still add?"; use `size_qty`/`size_unit` to judge whether it covers the amount; `kj`, `protein_g`, `fat_g`, `sat_fat_g`, `carb_g`, `sugars_g`, `fibre_g` and `sodium_mg` are per 100g where Woolies lists them, blank when it does not, so say "no nutrition data" rather than guess). Cupboard changes never touch the cart and are file-only (no browser): "I bought eggs elsewhere" -> `woolies cupboard add Eggs --qty 6 --source elsewhere --size 600g`; "I've run out of rice" -> `woolies cupboard remove rice`; "used one onion" -> `woolies cupboard use "Onion Brown"`; fix a count with `set-qty`. Quantities are whole numbers. `woolies cupboard clear` needs their explicit yes. After they collect an order, offer `woolies cupboard add-latest --dry-run`, then the real run after their yes. `woolies orders --latest` shows the newest order with items in one call (its `status` can lag the app, so ask them whether it has been collected).
2. **Resolve products.** Items that arrive with a stockcode: re-check availability and price (`woolies product` or a search). Everything else: one batched `woolies search` (use `--special`, compare `cupString`, ignore `isMarketProduct` and unavailable). Offer a few options only when the choice is not obvious; otherwise pick the cheapest sensible one.
3. **Confirm.** Show a short table (item, qty, price, stockcode) and the estimated cart total; flag it if the total is under the minimum spend in their notes. Do not touch the cart until the user says yes in chat. A simple "add butter" still gets this one-line confirmation.
4. **Fill the cart.** `woolies cart` (skip items already there by stockcode; `add` sets quantity, it does not increment; report unlisted items, never remove them unasked), `woolies store` (it must be the store named in their notes, else stop and ask), one `woolies add-many`, then `woolies cart` and report the total and any errors.
5. **Checkout.** The user does the time slot and payment themselves on the website. The CLI has no checkout command; if one is added later, still confirm item list, total and slot in chat before paying.
6. If the items came from a recipe, report the resolved stockcodes back so `/add-recipe` can save them in the recipe file.

Preferences: cheap, filling, homely; compare unit prices; specials first (their notes may say more). The live cart is the source of truth; there is no separate list file.

## Planning meals around specials
When the user wants meals built from what is on special, start from the deals rather than from a recipe:
1. Run `woolies specials --all --food --department "meat,fruit,dairy,freezer,deli,bakery,pantry" --limit 0 > <scratchpad>/specials.json` with a 5-minute Bash timeout, as it may need to fetch (around 2 minutes). Don't run it alongside another woolies call. The output can be over 1 MB, so search and read the file in parts. Later calls answer from the cache, so narrow with other filters (`--department meat --min-discount 20`) rather than `--refresh`.
2. If the result has `incomplete: true`, or a `lists` entry has `truncated: true`, tell the user that some deals are missing. Check `storeId` is the store in their notes, and mention `cache.ageMinutes` when the prices are more than a few hours old. Use `--refresh` if the store is wrong or they want the latest prices.
3. Build meals from proteins and vegetables first, then fill in from the rest. Fresh items are the scarcest on the list, and a low `--limit` would hide them because the top of the list is mostly half-price snacks. Check `departments` and `categories` as well as `discountPercent`.
4. A `multibuy` discount only applies when buying `multibuy.quantity`, so only count it if the meals use that many. A small `price-drop` barely counts as a special, so use your judgement on low percentages.
5. If an ingredient isn't on special, say so in the meal plan rather than treating it as a deal. Staples from "Always at home" don't need buying at all.
6. Hand the chosen items to the normal flow above. Their stockcodes still need the confirm step before anything goes in the cart.

## Rules
- Never check out or pay without the user's explicit confirmation in chat.
- Never change the cart (add, set quantity, remove) without their yes in chat.
- Never run woolies calls in parallel.
- Never log in for the user; ask them to run `woolies login`. Never log them out unless they ask.
- Never print or store credentials or cookies.
