# Attribution context

Read design section 11.4 before changes. Values are bounded, UTF-8 validated and
copied on context insertion/retrieval. Attributes require exact assembly keys
owned by linked modules. This package authenticates nobody; raw headers never
establish subject/tenant. Runtime correlation updates retain trusted identity and
attributes. Public signatures expose no internal types. Run attribution/event,
dispatcher and external-consumer race contracts when changing this boundary.
