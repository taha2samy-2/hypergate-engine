import os
import time
import threading
from concurrent import futures
import grpc
from kubernetes import client, config
from kubernetes.client.rest import ApiException

import envoy.service.auth.v3.external_auth_pb2_grpc as ext_authz_grpc
import envoy.service.auth.v3.external_auth_pb2 as ext_authz_proto
import google.rpc.status_pb2 as status

CONFIGMAP_NAME = "hyper-engine-config"
NAMESPACE = "default"

class AuthorizationServer(ext_authz_grpc.AuthorizationServicer):
    def Check(self, request, context):
        response = ext_authz_proto.CheckResponse()
        response.status.code = status.Status(code=0).code
        response.ok_response.CopyFrom(ext_authz_proto.OkHttpResponse())
        return response

def print_configmap_loop():
    try:
        config.load_incluster_config()
    except Exception:
        try:
            config.load_kube_config()
        except Exception:
            return

    v1 = client.CoreV1Api()
    while True:
        try:
            config_map = v1.read_namespaced_config_map(name=CONFIGMAP_NAME, namespace=NAMESPACE)
            yaml_content = config_map.data.get("config.yaml")
            if yaml_content:
                print("\n==================================================")
                print(" 📡 LIVE CONFIGMAP RECEIVED FROM OPERATOR (IN-RAM):")
                print("==================================================")
                print(yaml_content)
                print("==================================================\n")
        except ApiException as e:
            print(f"Waiting for ConfigMap '{CONFIGMAP_NAME}' to be created by Operator...")
        time.sleep(10)

def serve():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    ext_authz_grpc.add_AuthorizationServicer_to_server(AuthorizationServer(), server)
    server.add_insecure_port('[::]:9001')
    server.start()
    
    t = threading.Thread(target=print_configmap_loop, daemon=True)
    t.start()
    
    server.wait_for_termination()

if __name__ == '__main__':
    serve()