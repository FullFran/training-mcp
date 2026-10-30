# SI formula

Each set stores the deterministic descriptive index:

```text
SI = max(0, 0.2 * RPE - 0.6)
```

RPE must be between 1 and 10. The value is calculated on insert and recalculated whenever a set's RPE changes. Session totals are the sum of current set SI values; they are not stored separately.

Examples:

| RPE | SI |
|---:|---:|
| 1 | 0 |
| 2 | 0 |
| 8 | 1.0 |
| 10 | 1.4 |

SI is a product policy index for tracking consistency. It is not presented as a clinical or physiological measurement.

## Unknown RPE

A set logged without an RPE is stored with RPE 8, the value whose SI is exactly 1, and `rpe_imputed` set. It counts as one plain unit, weighted neither up nor down, which keeps totals comparable. The stored RPE is a placeholder: outputs carry `rpe_imputed`, the web app shows `@?` and does not prefill it, and `suggest_load` does not judge effort from it. Stating an RPE later through `update_set` clears the mark.
