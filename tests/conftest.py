import pytest
import requests
import redis
import time
import json

ENVOY_BASE_URL = "http://localhost:8080"
REQUEST_TIMEOUT = 10

@pytest.fixture(scope="session")
def redis_client():
	client = redis.Redis(host="localhost", port=6379, db=0, decode_responses=True)
	yield client
	for key in client.scan_iter("user_meta:*"):
		client.delete(key)
	client.close()

@pytest.fixture
def session():
	s = requests.Session()
	yield s
	s.close()

@pytest.fixture
def get_response_headers():
	def _get(response):
		return {k.lower(): v for k, v in response.headers.items()}
	return _get

@pytest.fixture
def extract_received_headers():
	def _extract(response):
		try:
			data = response.json()
		except ValueError as exc:
			pytest.fail(f"Failed to parse echo-server response as JSON: {exc}")
		received = data.get("request", {}).get("headers", {}) or data.get("headers", {})
		return {k.lower(): v for k, v in received.items()}
	return _extract

def test_doctor_enrichment_and_limit(session, redis_client, get_response_headers, extract_received_headers):
	current_sec = time.localtime().tm_sec
	wait_time = (60 - current_sec) % 60
	if wait_time < 5:
		wait_time += 60
	time.sleep(wait_time)

	user_id = "doctor-user"
	redis_key = f"user_meta:{user_id}"
	user_payload = {
		"profile": {
			"tier": "doctor"
		},
		"status": "active"
	}

	redis_client.set(redis_key, json.dumps(user_payload))

	try:
		headers = {"X-User-ID": user_id}

		for i in range(3):
			resp = session.get(f"{ENVOY_BASE_URL}/metadata", headers=headers, timeout=REQUEST_TIMEOUT)
			assert resp.status_code == 200
			
			resp_headers = get_response_headers(resp)
			assert int(resp_headers.get("ratelimit-remaining")) == 3 - (i + 1)
			
			upstream_headers = extract_received_headers(resp)
			assert upstream_headers.get("x-user-tier") == "doctor"
			assert "active" in upstream_headers.get("x-user-full-data")

		resp = session.get(f"{ENVOY_BASE_URL}/metadata", headers=headers, timeout=REQUEST_TIMEOUT)
		assert resp.status_code == 429

	finally:
		redis_client.delete(redis_key)

def test_engineer_fallback_limit(session, redis_client, get_response_headers, extract_received_headers):
	time.sleep(5)

	current_sec = time.localtime().tm_sec
	wait_time = (60 - current_sec) % 60
	if wait_time < 5:
		wait_time += 60
	time.sleep(wait_time)

	user_id = "engineer-user"
	redis_key = f"user_meta:{user_id}"
	user_payload = {
		"profile": {
			"tier": "engineer"
		},
		"status": "active"
	}

	redis_client.set(redis_key, json.dumps(user_payload))

	try:
		headers = {"X-User-ID": user_id}

		resp = session.get(f"{ENVOY_BASE_URL}/metadata", headers=headers, timeout=REQUEST_TIMEOUT)
		assert resp.status_code == 200
		
		resp_headers = get_response_headers(resp)
		assert int(resp_headers.get("ratelimit-remaining")) == 0
		
		upstream_headers = extract_received_headers(resp)
		assert upstream_headers.get("x-user-tier") == "engineer"

		resp = session.get(f"{ENVOY_BASE_URL}/metadata", headers=headers, timeout=REQUEST_TIMEOUT)
		assert resp.status_code == 429

	finally:
		redis_client.delete(redis_key)

def test_anonymous_missing_header_fallback(session, redis_client, get_response_headers, extract_received_headers):
	time.sleep(5)

	current_sec = time.localtime().tm_sec
	wait_time = (60 - current_sec) % 60
	if wait_time < 5:
		wait_time += 60
	time.sleep(wait_time)

	redis_key = "user_meta:anonymous"
	user_payload = {
		"profile": {
			"tier": "guest"
		},
		"status": "restricted"
	}

	redis_client.set(redis_key, json.dumps(user_payload))

	try:
		resp = session.get(f"{ENVOY_BASE_URL}/metadata", timeout=REQUEST_TIMEOUT)
		assert resp.status_code == 200
		
		resp_headers = get_response_headers(resp)
		assert int(resp_headers.get("ratelimit-remaining")) == 0
		
		upstream_headers = extract_received_headers(resp)
		assert upstream_headers.get("x-user-tier") == "guest"
		assert "restricted" in upstream_headers.get("x-user-full-data")

		resp = session.get(f"{ENVOY_BASE_URL}/metadata", timeout=REQUEST_TIMEOUT)
		assert resp.status_code == 429

	finally:
		redis_client.delete(redis_key)