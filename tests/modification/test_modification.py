import pytest
import requests

ENVOY_BASE_URL = "http://localhost:8080"
MODIFY_ENDPOINT = f"{ENVOY_BASE_URL}/api/modify"
REQUEST_TIMEOUT = 10


def extract_received_headers(response: requests.Response) -> dict[str, str]:
    try:
        data = response.json()
    except ValueError as exc:
        pytest.fail(f"Failed to parse echo-server response as JSON: {exc}")
    received = (
        data.get("request", {}).get("headers", {})
        or data.get("headers", {})
    )
    if not received:
        pytest.fail(f"Could not locate headers in echo-server response: {str(data)[:1000]}")
    return {k.lower(): v for k, v in received.items()}


def get_response_headers(response: requests.Response) -> dict[str, str]:
    return {k.lower(): v for k, v in response.headers.items()}


class TestHeaderModification:
    @pytest.fixture(scope="class")
    def modified_response(self) -> requests.Response:
        client_headers = {
            "Authorization": "Bearer secret-customer-token",
            "x-user-id": "999",
        }
        return requests.get(MODIFY_ENDPOINT, headers=client_headers, timeout=REQUEST_TIMEOUT)

    def test_http_status_is_200(self, modified_response: requests.Response):
        assert modified_response.status_code == 200

    def test_authorization_header_is_removed(self, modified_response: requests.Response):
        received_headers = extract_received_headers(modified_response)
        assert "authorization" not in received_headers

    def test_x_user_id_is_overridden(self, modified_response: requests.Response):
        received_headers = extract_received_headers(modified_response)
        assert "x-user-id" in received_headers
        assert received_headers["x-user-id"] == "123"

    def test_x_auth_status_is_added(self, modified_response: requests.Response):
        received_headers = extract_received_headers(modified_response)
        assert "x-auth-status" in received_headers
        assert received_headers["x-auth-status"] == "verified"


class TestHeaderSplitModifier:
    def test_header_split_logic(self):
        url = f"{ENVOY_BASE_URL}/api/test/split"
        resp = requests.get(url, headers={"x-debug-key": "secret"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        backend = extract_received_headers(resp)
        client = get_response_headers(resp)

        assert backend.get("x-flow-id") == "internal-123"
        assert "x-debug-key" not in backend
        assert client.get("x-flow-id") == "external-abc"
        assert client.get("x-processed-by") == "hyper-engine"


class TestHeaderConflictResolution:
    def test_header_revival(self):
        url = f"{ENVOY_BASE_URL}/api/test/conflict"
        resp = requests.get(url, headers={"x-test-conflict": "original"}, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        backend = extract_received_headers(resp)
        assert backend.get("x-test-conflict") == "revived"

    def test_upstream_downstream_split(self):
        url = f"{ENVOY_BASE_URL}/api/test/splitv2"
        resp = requests.get(url, timeout=REQUEST_TIMEOUT)
        assert resp.status_code == 200

        backend = extract_received_headers(resp)
        client = get_response_headers(resp)

        assert backend.get("x-version") == "v1-internal"
        assert client.get("x-version") == "v1-public"


class TestSmokeChecks:
    def test_envoy_is_reachable(self):
        try:
            response = requests.get(MODIFY_ENDPOINT, timeout=REQUEST_TIMEOUT)
        except requests.ConnectionError as exc:
            pytest.fail(f"Cannot reach Envoy at {MODIFY_ENDPOINT}: {exc}")
        assert response.status_code in (200, 403, 404, 503)
