#!/usr/bin/env python3
import sys
import re
import subprocess
import json
import argparse

def get_hash(repo, ref):
    if re.match(r'^[0-9a-f]{40}$', ref):
        return ref
    cmd = ["gh", "api", f"repos/{repo}/commits/{ref}", "--jq", ".sha"]
    res = subprocess.run(cmd, capture_output=True, text=True)
    return res.stdout.strip() if res.returncode == 0 and res.stdout.strip() else ref

def get_tag(repo, ref):
    if not re.match(r'^[0-9a-f]{40}$', ref):
        return ref
    cmd = ["gh", "api", f"repos/{repo}/tags"]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode == 0:
        try:
            tags = json.loads(res.stdout)
            for tag in tags:
                if tag.get('commit', {}).get('sha') == ref:
                    return tag.get('name')
        except:
            pass
    return ref

def process_file(file_path, reverse=False):
    with open(file_path, 'r') as f:
        lines = f.readlines()

    pattern = re.compile(r'^(\s*uses:\s*)([^@]+)@([^\s]+)(.*)$')
    new_lines = []

    for line in lines:
        match = pattern.match(line)
        if match:
            prefix = match.group(1)
            action = match.group(2)
            ref = match.group(3)
            suffix = match.group(4)
            
            parts = action.split('/')
            if len(parts) >= 2:
                repo = f"{parts[0]}/{parts[1]}"
                new_ref = get_tag(repo, ref) if reverse else get_hash(repo, ref)
                line = f"{prefix}{action}@{new_ref}{suffix}\n"
        new_lines.append(line)

    with open(file_path, 'w') as f:
        f.writelines(new_lines)

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("file")
    parser.add_argument("--reverse", action="store_true")
    args = parser.parse_args()
    
    process_file(args.file, args.reverse)