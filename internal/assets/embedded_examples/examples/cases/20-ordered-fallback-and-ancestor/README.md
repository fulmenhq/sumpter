# 20 Ordered Fallback and Ancestor Navigation

Contrasts document-order XPath union selection (`(BetaEmail | AlphaEmail)[1]`)
with guarded priority fallback (`(BetaEmail | AlphaEmail[not(../BetaEmail)])[1]`),
and shows `ancestor::`, `preceding-sibling::`, and `item_mapping` parent
attribute (`../../@id`) navigation.
