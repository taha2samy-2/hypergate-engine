#!/usr/bin/env python3
import sys
import re
import subprocess
import argparse

def get_hash(repo, ref):
    if re.match(r'^[0-9a-f]{40}$', ref):
        print(f"SKIP: {repo}@{ref} is already a hash")
        return ref
    
    print(f"FETCH: Resolving hash for {repo}@{ref}...")
    cmd = ["gh", "api", f"repos/{repo}/commits/{ref}", "--jq", ".sha"]
    res = subprocess.run(cmd, capture_output=True, text=True)
    
    if res.returncode == 0 and res.stdout.strip():
        sha = res.stdout.strip()
        print(f"DONE: {ref} -> {sha}")
        return sha
        
    print(f"FAIL: Could not resolve hash for {repo}@{ref}")
    return ref

def get_tag(repo, ref):
    if not re.match(r'^[0-9a-f]{40}$', ref):
        print(f"SKIP: {repo}@{ref} is already a tag")
        return ref
        
    print(f"FETCH: Resolving tag for {repo}@{ref}...")
    jq_filter = f'.[] | select(.commit.sha == "{ref}") | .name'
    cmd = ["gh", "api", "--paginate", f"repos/{repo}/tags", "--jq", jq_filter]
    res = subprocess.run(cmd, capture_output=True, text=True)
    
    if res.returncode == 0 and res.stdout.strip():
        tags = res.stdout.strip().split('\n')
        best_tag = tags[0]
        print(f"DONE: {ref} -> {best_tag}")
        return best_tag
        
    print(f"FAIL: Could not resolve tag for {repo}@{ref}")
    return ref

def process_file(file_path, reverse=False):
    print(f"START: Processing {file_path}")
    print(f"MODE: {'Hash to Tag (Reverse)' if reverse else 'Tag to Hash'}")
    
    with open(file_path, 'r') as f:
        lines = f.readlines()

    pattern = re.compile(r'^(\s*uses:\s*)([^@]+)@([^\s]+)(.*)$')
    new_lines = []
    changes = 0

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
                
                if new_ref != ref:
                    line = f"{prefix}{action}@{new_ref}{suffix}\n"
                    changes += 1
                    
        new_lines.append(line)

    if changes > 0:
        with open(file_path, 'w') as f:
            f.writelines(new_lines)
        print(f"FINISH: Saved {changes} changes to {file_path}")
    else:
        print("FINISH: No changes needed")

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("file")
    parser.add_argument("--reverse", action="store_true")
    args = parser.parse_args()
    
    process_file(args.file, args.reverse)