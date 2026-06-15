import pytest
import requests

ENVOY_BASE_URL = "http://localhost:8080"
REQUEST_TIMEOUT = 10

def extract_received_headers(response: requests.Response) -> dict[str, str]:
    try:
        data = response.json()
    except ValueError as exc:
        pytest.fail(f"Failed to parse echo-server response: {exc}")
    received = data.get("request", {}).get("headers", {}) or data.get("headers", {})
    return {k.lower(): v for k, v in received.items()}

def get_response_headers(response: requests.Response) -> dict[str, str]:
    return {k.lower(): v for k, v in response.headers.items()}

class TestCorrelationId:
    def test_uuidv4_propagation(self):
        url = f"{ENVOY_BASE_URL}/api/test/uuidv4"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        returned = get_response_headers(resp)

        assert "x-request-id" in received
        assert "x-request-id" in returned

        val = received["x-request-id"]
        assert len(val) == 36
        assert val == returned["x-request-id"]

    def test_uuidv7_prefix(self):
        url = f"{ENVOY_BASE_URL}/api/test/uuidv7"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        assert "x-request-id-v7" in received
        val = received["x-request-id-v7"]
        assert val.startswith("v7-")
        assert len(val) == 3 + 36

    def test_ulid_format(self):
        url = f"{ENVOY_BASE_URL}/api/test/ulid"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        assert "x-request-id-ulid" in received
        val = received["x-request-id-ulid"]
        assert len(val) == 26

    def test_xid_format(self):
        url = f"{ENVOY_BASE_URL}/api/test/xid"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        assert "x-request-id-xid" in received
        val = received["x-request-id-xid"]
        assert len(val) == 20

    def test_id_preservation(self):
        url = f"{ENVOY_BASE_URL}/api/test/preserve"
        client_headers = {"x-correlation-id": "my-custom-id-123"}
        resp = requests.get(url, headers=client_headers, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        assert received.get("x-correlation-id") == "my-custom-id-123"

    def test_id_overwrite(self):
        url = f"{ENVOY_BASE_URL}/api/test/overwrite"
        client_headers = {"x-correlation-id": "my-custom-id-123"}
        resp = requests.get(url, headers=client_headers, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        received = extract_received_headers(resp)
        val = received.get("x-correlation-id")
        assert val is not None
        assert val != "my-custom-id-123"
        assert len(val) == 36

    def test_advanced_mapping_and_validation(self):
        url = f"{ENVOY_BASE_URL}/api/test/advanced"

        # Case A: Valid Input
        resp = requests.get(url, headers={"x-client-trace": "1234abcd"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        assert received.get("x-internal-id") == "1234abcd"
        assert returned.get("x-external-correlation") == "1234abcd"

        # Case B: Invalid Input
        resp = requests.get(url, headers={"x-client-trace": "NOT-HEX"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        val_in = received.get("x-internal-id")
        val_out = returned.get("x-external-correlation")
        assert val_in is not None
        assert val_in != "NOT-HEX"
        assert len(val_in) == 36
        assert val_in == val_out

        # Case C: Missing Input
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        val_in = received.get("x-internal-id")
        val_out = returned.get("x-external-correlation")
        assert val_in is not None
        assert len(val_in) == 36
        assert val_in == val_out

class TestCorrelationAdvanced:
    def test_renaming(self):
        url = f"{ENVOY_BASE_URL}/api/test/renaming"
        resp = requests.get(url, headers={"x-client-id": "client-val-123"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        assert received.get("x-internal-id") == "client-val-123"
        assert returned.get("x-final-id") == "client-val-123"

    def test_validation_success(self):
        url = f"{ENVOY_BASE_URL}/api/test/val-ok"
        resp = requests.get(url, headers={"x-request-id": "fixed-789"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        assert received.get("x-request-id") == "fixed-789"
        assert returned.get("x-request-id") == "fixed-789"

    def test_validation_failure(self):
        url = f"{ENVOY_BASE_URL}/api/test/val-fail"
        resp = requests.get(url, headers={"x-request-id": "bad-id"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        val_in = received.get("x-request-id")
        val_out = returned.get("x-request-id")
        assert val_in is not None
        assert val_in.startswith("gen-")
        assert len(val_in) == 4 + 36
        assert val_in == val_out

    def test_default_generation(self):
        url = f"{ENVOY_BASE_URL}/api/test/renaming"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200
        received = extract_received_headers(resp)
        returned = get_response_headers(resp)
        val_in = received.get("x-internal-id")
        val_out = returned.get("x-final-id")
        assert val_in is not None
        assert len(val_in) == 36
        assert val_in == val_out

class TestSmokeChecks:
    def test_envoy_is_reachable(self):
        try:
            response = requests.get(ENVOY_BASE_URL, timeout=REQUEST_TIMEOUT)
        except requests.ConnectionError as exc:
            pytest.fail(f"Cannot reach Envoy: {exc}")
        assert response.status_code in (200, 403, 404, 503)
