import hashlib
import time

import pytest
import redis
import requests

ENVOY_BASE_URL = "http://localhost:8080"
REQUEST_TIMEOUT = 10


def sha256_hex(raw_key: str) -> str:
    """Compute the SHA-256 hex digest of a raw API key string (matches engine hashing)."""
    return hashlib.sha256(raw_key.encode()).hexdigest()


@pytest.fixture(scope="session")
def redis_client():
    """Connect directly to the local Redis instance on port 6379."""
    client = redis.Redis(host="localhost", port=6379, db=0, decode_responses=True)
    yield client
    # Cleanup any apikey: keys seeded during the test lifecycle
    for key in client.scan_iter("apikey:*"):
        client.delete(key)
    client.close()


@pytest.fixture
def session():
    """Isolated requests session per test."""
    s = requests.Session()
    yield s
    s.close()


def extract_received_headers(response: requests.Response) -> dict[str, str]:
    """Extract headers received by the upstream echo-server from its JSON response."""
    try:
        data = response.json()
    except ValueError as exc:
        pytest.fail(f"Failed to parse echo-server response as JSON: {exc}")
    received = data.get("request", {}).get("headers", {}) or data.get("headers", {})
    return {k.lower(): v for k, v in received.items()}


def extract_received_path(response: requests.Response) -> str:
    """Extract the full request path seen by the upstream echo-server."""
    try:
        data = response.json()
    except ValueError:
        return ""
    return data.get("request", {}).get("url", "") or data.get("url", "")


# ---------------------------------------------------------------------------
# Test 1: Plain format — semicolon-delimited Redis string
# ---------------------------------------------------------------------------
def test_api_key_plain_format(session, redis_client):
    """
    Pre-seed: apikey:sha256("test-key-plain") -> "999;doctor;taha"
    Expect: X-Consumer-ID=999, X-User-Tier=doctor injected upstream.
    The x-api-key header must NOT appear upstream (hide_credentials=true).
    """
    raw_key = "test-key-plain"
    redis_key = f"apikey:{sha256_hex(raw_key)}"
    redis_client.set(redis_key, "999;doctor;taha")

    try:
        resp = session.get(
            f"{ENVOY_BASE_URL}/api/apikey/plain",
            headers={"x-api-key": raw_key},
            timeout=REQUEST_TIMEOUT,
        )
        assert resp.status_code == 200, f"Expected 200 but got {resp.status_code}: {resp.text}"

        upstream = extract_received_headers(resp)
        assert upstream.get("x-consumer-id") == "999", f"Got: {upstream}"
        assert upstream.get("x-user-tier") == "doctor", f"Got: {upstream}"
        # Credentials must be stripped
        assert "x-api-key" not in upstream, "API key must be stripped from upstream headers"
    finally:
        redis_client.delete(redis_key)


# ---------------------------------------------------------------------------
# Test 2: Hash format — HMGET specific fields from Redis hash
# ---------------------------------------------------------------------------
def test_api_key_hash_format(session, redis_client):
    """
    Pre-seed: apikey:sha256("test-key-hash") -> {client_id: "777", tier: "vip", username: "taha"}
    Expect: X-Consumer-ID=777, X-User-Tier=vip injected upstream.
    """
    raw_key = "test-key-hash"
    redis_key = f"apikey:{sha256_hex(raw_key)}"
    redis_client.hset(redis_key, mapping={"client_id": "777", "tier": "vip", "username": "taha"})

    try:
        resp = session.get(
            f"{ENVOY_BASE_URL}/api/apikey/hash",
            headers={"x-api-key": raw_key},
            timeout=REQUEST_TIMEOUT,
        )
        assert resp.status_code == 200, f"Expected 200 but got {resp.status_code}: {resp.text}"

        upstream = extract_received_headers(resp)
        assert upstream.get("x-consumer-id") == "777", f"Got: {upstream}"
        assert upstream.get("x-user-tier") == "vip", f"Got: {upstream}"
        assert "x-api-key" not in upstream, "API key must be stripped from upstream headers"
    finally:
        redis_client.delete(redis_key)


# ---------------------------------------------------------------------------
# Test 3: JSON format — gjson path extraction
# ---------------------------------------------------------------------------
def test_api_key_json_format(session, redis_client):
    """
    Pre-seed: apikey:sha256("test-key-json") -> '{"profile":{"email":"taha@example.com"}}'
    Expect: X-Consumer-Email=taha@example.com injected upstream.
    """
    raw_key = "test-key-json"
    redis_key = f"apikey:{sha256_hex(raw_key)}"
    redis_client.set(redis_key, '{"profile":{"email":"taha@example.com"}}')

    try:
        resp = session.get(
            f"{ENVOY_BASE_URL}/api/apikey/json",
            headers={"x-api-key": raw_key},
            timeout=REQUEST_TIMEOUT,
        )
        assert resp.status_code == 200, f"Expected 200 but got {resp.status_code}: {resp.text}"

        upstream = extract_received_headers(resp)
        assert upstream.get("x-consumer-email") == "taha@example.com", f"Got: {upstream}"
        assert "x-api-key" not in upstream, "API key must be stripped from upstream headers"
    finally:
        redis_client.delete(redis_key)


# ---------------------------------------------------------------------------
# Test 4: Status check — suspended account must be 403
# ---------------------------------------------------------------------------
def test_api_key_status_check_failed(session, redis_client):
    """
    Pre-seed: hash with status="suspended" (not "active").
    Expect: 403 Forbidden with body containing the suspension message.
    """
    raw_key = "test-key-suspended"
    redis_key = f"apikey:{sha256_hex(raw_key)}"
    redis_client.hset(redis_key, mapping={"client_id": "888", "status": "suspended"})

    try:
        resp = session.get(
            f"{ENVOY_BASE_URL}/api/apikey/status",
            headers={"x-api-key": raw_key},
            timeout=REQUEST_TIMEOUT,
        )
        assert resp.status_code == 403, f"Expected 403 but got {resp.status_code}: {resp.text}"
        assert "suspended" in resp.text.lower(), (
            f"Expected suspension message in body, got: {resp.text}"
        )
    finally:
        redis_client.delete(redis_key)


# ---------------------------------------------------------------------------
# Test 5: Credentials hiding — key must not appear in headers or query string
# ---------------------------------------------------------------------------
def test_api_key_credentials_hiding(session, redis_client):
    """
    Pre-seed: apikey:sha256("test-key-plain") -> "100;nurse;hidden"
    Send request with x-api-key header AND ?x-api-key=... query param.
    Expect: 200, key absent from upstream headers AND upstream path.
    """
    raw_key = "test-key-hidden"
    redis_key = f"apikey:{sha256_hex(raw_key)}"
    redis_client.set(redis_key, "100;nurse;hidden")

    try:
        resp = session.get(
            f"{ENVOY_BASE_URL}/api/apikey/plain",
            headers={"x-api-key": raw_key},
            params={"x-api-key": raw_key},
            timeout=REQUEST_TIMEOUT,
        )
        assert resp.status_code == 200, f"Expected 200 but got {resp.status_code}: {resp.text}"

        # The API key must NOT appear in upstream headers
        upstream = extract_received_headers(resp)
        assert "x-api-key" not in upstream, "API key must be stripped from upstream headers"

        # The API key must NOT appear in the path the upstream received
        upstream_path = extract_received_path(resp)
        assert raw_key not in upstream_path, (
            f"API key found in upstream path: {upstream_path}"
        )
    finally:
        redis_client.delete(redis_key)


# ---------------------------------------------------------------------------
# Test 6: Missing key -> 401; invalid key -> 401; L1 cache blocks repeat quickly
# ---------------------------------------------------------------------------
def test_api_key_missing_and_invalid(session, redis_client):
    """
    6a: No API key at all -> 401.
    6b: Key present but not in Redis -> 401 on first attempt.
    6c: Immediate second attempt with same invalid key must also be 401
        (served from L1 cache, verifiably fast).
    """
    # 6a — Missing API key entirely
    resp = session.get(
        f"{ENVOY_BASE_URL}/api/apikey/hash",
        timeout=REQUEST_TIMEOUT,
    )
    assert resp.status_code == 401, (
        f"Expected 401 for missing key, got {resp.status_code}: {resp.text}"
    )
    assert "missing" in resp.text.lower() or "unauthorized" in resp.text.lower(), (
        f"Expected missing-key message, got: {resp.text}"
    )

    # 6b — Key that does not exist in Redis
    ghost_key = "ghost-key-does-not-exist-xyz-99999"
    # Ensure it truly is not seeded
    redis_client.delete(f"apikey:{sha256_hex(ghost_key)}")

    resp = session.get(
        f"{ENVOY_BASE_URL}/api/apikey/hash",
        headers={"x-api-key": ghost_key},
        timeout=REQUEST_TIMEOUT,
    )
    assert resp.status_code == 401, (
        f"Expected 401 for unknown key, got {resp.status_code}: {resp.text}"
    )

    # 6c — Immediate second request: the L1 cache should block it just as fast
    t_start = time.monotonic()
    resp2 = session.get(
        f"{ENVOY_BASE_URL}/api/apikey/hash",
        headers={"x-api-key": ghost_key},
        timeout=REQUEST_TIMEOUT,
    )
    elapsed_ms = (time.monotonic() - t_start) * 1000

    assert resp2.status_code == 401, (
        f"Expected 401 (cached block), got {resp2.status_code}: {resp2.text}"
    )
    # Cached block should be served in well under 50ms (no Redis round-trip)
    assert elapsed_ms < 50, (
        f"L1 cache block too slow ({elapsed_ms:.1f}ms); expected < 50ms"
    )
