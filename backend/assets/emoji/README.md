# Class and role icons

Original FairShare artwork (no game assets): a tile in the class colour with a bold glyph, 128×128 PNG. The bot uploads them as application emoji at startup and uses them in signup and roster embeds.

Regenerate after editing the drawings:

```
python tools/icons/make_icons.py --sheet    # also writes dist/icons.png for a quick look
```

Names: `warrior` `paladin` `hunter` `rogue` `priest` `shaman` `mage` `warlock` `druid` `tank` `healer` `melee` `ranged` `absent`. Override the folder with `FAIRSHARE_EMOJI_DIR`. A missing file falls back to a plain Unicode symbol.
