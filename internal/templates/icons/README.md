# The catalogue's logos

One file per template, named after the template it belongs to. A template with
no file here shows the first letter of its name instead, which is what the whole
catalogue looked like before these existed.

## Where they come from

[homarr-labs/dashboard-icons](https://github.com/homarr-labs/dashboard-icons),
the collection Homarr, Homepage and Dashy all draw on. The collection is
**CC0-1.0** — it waives its own rights, which is what makes vendoring it here
possible at all.

**The logos themselves are trademarks of the projects they belong to.** They are
used to identify the software a catalogue entry installs, which is what a
catalogue entry is for, and nothing here claims any right in them. A project
that would rather not appear should say so and its file will be removed.

## Why they are committed rather than fetched

The panel's Content-Security-Policy says `img-src 'self'`. Loading them from a
CDN would mean widening it, and three things follow from that:

* The CDN learns which self-hosted apps each of your users is browsing.
* An install with no outbound network has a catalogue with no pictures.
* Somebody else's uptime becomes a thing that makes this product look broken.

2.5 MB inside a 40 MB binary is the price, and it buys all three back.

## Refreshing them

```sh
python3 hack/fetch_icons.py            # fetch what is missing
python3 hack/fetch_icons.py --report   # say what is missing, change nothing
```

It matches a template's id against the collection, trying the name on its card
and the part before `-with-` as well — `forgejo-with-mariadb` is Forgejo, and
the database it was paired with is not part of its identity. SVG where the
collection has one, WebP where it does not: the same picture as the PNG at
roughly a third of the bytes, and a 260 KB logo drawn at 40 pixels is a waste
nobody sees and everybody carries.

71 of the 282 templates have no logo in the collection. They show a letter, and
adding one is adding a file here with the right name.
