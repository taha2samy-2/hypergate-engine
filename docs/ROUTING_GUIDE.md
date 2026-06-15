

# Matcher Logic Guide: Hyper Gateway Engine

Welcome to the **Matcher Logic Guide**. This document provides a comprehensive reference on how to configure request matching, path prefixes, regular expressions, header checks, and complex boolean logic in your `config.yaml`.

---

## 1. The Boolean Evaluation Model (AND / OR)

To keep the configuration clean and highly optimized, the routing engine implements the **"Matches List = OR, Fields within Matcher = AND"** architectural paradigm.

```text
Route Configuration
  ├── matches (List) ──► Evaluated as OR (If ANY match block is true, route is selected)
        ├── Match Block 1 (AND) ──► (Path AND Headers AND Method) must match
        └── Match Block 2 (AND) ──► (Path AND Headers) must match
```

*   **AND Logic (Within a single block):** All defined criteria inside a single match block (e.g., path prefix *and* headers) must evaluate to `true` for that block to succeed.
*   **OR Logic (Between blocks):** If your route defines a list of `matches`, the router evaluates them sequentially. If **any** match block in the list evaluates to `true`, the target chain is selected immediately (**First Match Wins**).

---

## 2. Path Matching Options

The engine supports two high-performance path matching strategies. You should define only one per match block:

### A. Path Prefix (`path_prefix`)
Checks if the incoming request URI path starts with the specified literal string.
*   **Performance:** Extremely fast, executing in `O(1)` complexity using Go's `strings.HasPrefix`.
*   **Example:** `path_prefix: "/api/v1"`
    *   ✅ Matches: `/api/v1`, `/api/v1/users`, `/api/v1/products/search`.
    *   ❌ Does not match: `/api`, `/v1/api`.

### B. Path Regular Expression (`path_regex_pattern`)
Matches the request URI path against a standard regular expression.
*   **Performance:** Pre-compiled during application bootstrap (boot-time), resulting in zero dynamic allocations during request runtime.
*   **Example:** `path_regex_pattern: "^/api/v[2-9]/users$"`
    *   ✅ Matches: `/api/v2/users`, `/api/v5/users`.
    *   ❌ Does not match: `/api/v1/users`, `/api/v2/users/profile`.

---

## 3. Header Matching Options

Header matching evaluates incoming HTTP headers. Every key-value pair specified in the `headers` block is evaluated using `AND` logic.

### ⚠️ CRITICAL: Envoy Header Lowercasing
Envoy Proxy automatically **lowercases all HTTP header keys** before transmitting them (e.g., `X-Client-ID` becomes `x-client-id`).
*   **Requirement:** You **MUST** specify all header keys in **lowercase** inside your `config.yaml`.

### Matching Types:
1.  **Exact Match:** Checks if the value exactly matches the specified string (case-sensitive).
    ```yaml
    headers:
      "x-environment": "staging" # Key must be lowercase!
    ```
2.  **Existence Check (Wildcard `*`):** Checks if the header key is present, regardless of its value.
    *   **Performance:** Ultra-fast `O(1)` hash map lookup.
    ```yaml
    headers:
      "x-client-id": "*" 
    ```
3.  **Regular Expression (`regex_pattern`):** Matches the header value against a pre-compiled regex.
    ```yaml
    headers:
      "x-api-version":
        regex_pattern: "^v[2-9]$"
    ```

---

## 4. Quick Reference Matrix

| Configuration Field | Target | Match Type | Evaluation | Complexity |
| :--- | :--- | :--- | :--- | :--- |
| `path_prefix` | URI Path | Literal Prefix | AND | `O(1)` - Fastest |
| `path_regex_pattern` | URI Path | Pre-compiled Regex | AND | `O(N)` - Fast |
| `headers` (Key) | Header Key | Existence (`*`) | AND | `O(1)` - Instant |
| `headers -> value` | Header Value | Exact String | AND | `O(1)` - Fast |
| `headers -> regex` | Header Value | Pre-compiled Regex | AND | `O(N)` - Fast |

---

## 5. Fallback Routing (`other`)

The `other` field is the **mandatory** catch-all fallback. If an incoming request matches none of the configured routes, the engine automatically routes it to this chain.

```yaml
router:
  routes:
    - target_chain: "chain_secure_auth"
      matches:
        - path_prefix: "/api/users"
  
  # Fallback: Executed when no matches succeed (e.g., Default Deny)
  other: "chain_fallback_deny"
```

---

## 6. Advanced Routing Examples

### Example 1: Path OR Header
Route to `chain_public_tracing` if the path starts with `/api/public` **OR** if the request contains `x-debug: true`:
```yaml
- target_chain: "chain_public_tracing"
  matches:
    - path_prefix: "/api/public"
    - headers:
        "x-debug": "true"
```

### Example 2: Compound Matching (AND + OR)
Route to `chain_secure_auth` if:
(Path starts with `/api/users` **AND** header `x-client` exists) **OR** (Path matches regex `^/api/v[2-9]/users$`).
```yaml
- target_chain: "chain_secure_auth"
  matches:
    - path_prefix: "/api/users"
      headers:
        "x-client": "*"
    - path_regex_pattern: "^/api/v[2-9]/users$"
```

---

## 7. Performance Best Practices

To maintain sub-millisecond execution times under heavy traffic (e.g., 50k+ QPS):

1.  **Prefer `path_prefix` over Regex:** Prefix checking is CPU-friendly. Use regex only when necessary.
2.  **Order Matters:** Put the most frequently matched blocks at the **top** of the `matches` list to enable early exit.
3.  **Use Wildcards for Presence:** Use `"header_name": "*"` instead of `.*` regex if you only need to check if a header exists.

---

## 8. Diagnostic Logs & Troubleshooting

When running in `DEBUG` mode, you can trace the matcher logic in the logs:

*   **Success Match:**
    ```text
    DEBUG [Router] Request matched route rule {"rule_name": "User Policy", "target_chain": "chain_secure_auth"}
    ```
*   **No Match (Fallback):**
    ```text
    DEBUG [Router] No route rule matched, falling back to 'other' {"fallback_chain": "chain_fallback_deny"}
    INFO  [Executor] Request blocked by filter chain {"status_code": 403, "path": "/api/unknown"}
    ```