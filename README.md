<p align="center">
  <img src="docs/assets/banner.jpg" alt="TOMB" width="640">
</p>

<h1 align="center">TOMB Guild Platform</h1>

<p align="center">
  <em>Sign in with Battle.net. See who you are playing right now.</em>
</p>

---

**<https://tombguild.com>**

The TOMB guild site. Sign in with your Battle.net account and your characters
are already there — pulled straight from Blizzard, nothing to type in and
nothing to keep up to date by hand.

It is for TOMB members. Membership is checked against the guild your characters
are actually in, so there is no list for anyone to maintain and nobody to ask
for access.

## Signing in

Click **Sign in with Battle.net**. Blizzard's own page asks whether you want to
let the site see your World of Warcraft profile; approve it and you land back
here, signed in.

You never type a password into this site. It never sees one, never stores one,
and cannot change anything on your account — it can only read your characters.
If you ever want to cut it off, revoke it in your Battle.net account settings
and it loses access immediately.

You stay signed in for about a day, then sign in again the same way.

## My Characters

Every character on your account is listed down the left, most recently played
first.

- **Hover a character** — or tab to it with the keyboard — and its card opens.
- **Click the name** in that card and the main panel switches to it.

The main panel is the Armory-style view: your character as Blizzard renders it,
in the gear it is wearing right now, alongside its realm and guild, class and
specialisation, level and item level, and every equipped slot with its own item
level and quality colour.

It opens on whatever you played last, so the common case takes no clicks at all.

A few things worth knowing:

- **It is always live.** Nothing is cached. Every time you open the page it asks
  Blizzard again, so what you see is what Blizzard knows now. Log out of the
  game in new gear and it is here on your next refresh.
- **Characters you have deleted or transferred away may still be listed by
  Blizzard.** Those are skipped quietly rather than shown as errors.
- **Not every character has a render.** If Blizzard has never generated an image
  for one, the panel says so and shows everything else as normal.

## Coming to TOMB

None of this is built yet — it is what the site is being pointed at. There is a
**Coming Soon** page in the top navigation with the same list.

**Guildmates' characters.** The same view for anyone in the guild: who has been
raiding on what, who has an alt geared for the slot you are short this week, who
has not logged in since the patch dropped.

**Guild calendar.** Raid nights, key pushes and transmog runs in one place, with
sign-ups that survive being scrolled past in Discord.

**Ask TOMB Bot** *(AI)*. An agent that knows *this* guild. Ask what is running
this week, who normally tanks, what the loot rules are, or for advice on a spec
you have not touched in a year — answered from the guild's own data rather than
from World of Warcraft in general.

**Combat log analysis** *(AI)*. Compare your logs against the top performers of
your class and spec. Not a number and a ranking, but the actual differences,
ordered by what each one is costing you, with a suggested fix for each.

**Gear analysis** *(AI)*. Compare your gear to the top performers and get the
path of least resistance to your next upgrades: which slot is holding you back
most, where the piece comes from, and how much effort it is — prioritised, so
your crests, catalyst charges and vault picks go where they matter rather than
where you happened to look first.

No dates. When something lands it will appear in the navigation.

## Something not right?

**"TOMB members only" but you are in TOMB.** Membership is read from Blizzard,
so a character that joined very recently can take a little while to show up
there. If it persists, say so in Discord — it is worth looking at rather than
waiting out.

**A character missing, or the wrong gear.** The site shows exactly what Blizzard
returns, and Blizzard updates a character after you log out of the game. The
usual fix is to log out and refresh.

---

Built and run by the guild. How it works, how it is deployed and how to add to
it are in [docs/README.md](docs/README.md).
