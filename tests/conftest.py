import pytest
import requests
import time

@pytest.fixture(scope="session")
def session() -> requests.Session:
    """A shared requests.Session for all tests."""
    s = requests.Session()
    yield s
    s.close()

@pytest.fixture(scope="session")
def extract_received_headers():
    """Extract headers received by the backend-service (echo-server)."""
    def _extract(response: requests.Response) -> dict[str, str]:
        try:
            data = response.json()
        except ValueError as exc:
            pytest.fail(f"Failed to parse echo-server response as JSON: {exc}")
        
        # Check both nested and top-level headers depending on echo-server version
        received = (
            data.get("request", {}).get("headers", {})
            or data.get("headers", {})
        )
        if not received:
            pytest.fail(f"Could not locate headers in echo-server response: {str(data)[:1000]}")
        return {k.lower(): v for k, v in received.items()}
    return _extract

@pytest.fixture(scope="session")
def get_response_headers():
    """Extract headers returned directly in the HTTP response."""
    def _get(response: requests.Response) -> dict[str, str]:
        return {k.lower(): v for k, v in response.headers.items()}
    return _get

@pytest.fixture(scope="class", autouse=True)
def wait_for_engine_ready():
    """A helper to wait for the Envoy and hyper-engine to be ready before each test class."""
    base_url = "http://localhost:8080"
    max_attempts = 15
    for _ in range(max_attempts):
        try:
            # Simple GET request to healthcheck or root
            resp = requests.get(f"{base_url}/healthz", timeout=1)
            # Any HTTP status means the Envoy proxy is responding
            if resp.status_code in (200, 403, 404, 503):
                return
        except requests.RequestException:
            pass
        time.sleep(1)
    pytest.fail("Timeout waiting for Envoy/hyper-engine to be ready")
