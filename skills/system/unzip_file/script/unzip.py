#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Author:    Vijay
# Email:     hustwujing@163.com
# Date:      2026
# Copyright: Copyright (c) 2026 Vijay
#
"""
Unzip Assistant Script
Supported formats: zip, tar, tar.gz, tgz, rar, 7z
"""

import sys
import os
import hashlib
import subprocess
import json
from pathlib import Path


def get_bucket(filename: str) -> str:
    """Generate a bucket name from the second character of the filename's MD5"""
    md5 = hashlib.md5(filename.encode()).hexdigest()
    return md5[1].upper()


def get_output_dir(archive_path: str) -> str:
    """Generate the extraction output directory"""
    filename = os.path.basename(archive_path)

    # Strip the extension
    name = filename
    for ext in ['.tar.gz', '.tgz', '.tar', '.zip', '.rar', '.7z']:
        if name.lower().endswith(ext):
            name = name[:-len(ext)]
            break

    bucket = get_bucket(filename)
    output_dir = f"output/{bucket}/{name}_unzipped"
    return output_dir


def detect_format(archive_path: str) -> str:
    """Detect the archive format"""
    lower = archive_path.lower()
    if lower.endswith('.tar.gz') or lower.endswith('.tgz'):
        return 'tar.gz'
    elif lower.endswith('.tar'):
        return 'tar'
    elif lower.endswith('.zip'):
        return 'zip'
    elif lower.endswith('.rar'):
        return 'rar'
    elif lower.endswith('.7z'):
        return '7z'
    else:
        return 'unknown'


def unzip_file(archive_path: str, output_dir: str, fmt: str) -> tuple:
    """Perform the extraction and return (success, message)"""

    # Create the output directory
    os.makedirs(output_dir, exist_ok=True)

    try:
        if fmt == 'zip':
            result = subprocess.run(
                ['unzip', '-o', '-q', archive_path, '-d', output_dir],
                capture_output=True, text=True
            )
        elif fmt == 'tar.gz':
            result = subprocess.run(
                ['tar', '-xzf', archive_path, '-C', output_dir],
                capture_output=True, text=True
            )
        elif fmt == 'tar':
            result = subprocess.run(
                ['tar', '-xf', archive_path, '-C', output_dir],
                capture_output=True, text=True
            )
        elif fmt == 'rar':
            result = subprocess.run(
                ['unrar', 'x', '-o+', '-y', archive_path, output_dir + '/'],
                capture_output=True, text=True
            )
        elif fmt == '7z':
            result = subprocess.run(
                ['7z', 'x', '-y', f'-o{output_dir}', archive_path],
                capture_output=True, text=True
            )
        else:
            return False, f"Unsupported format: {fmt}"

        if result.returncode != 0:
            error_msg = result.stderr or result.stdout or "Extraction failed"
            return False, error_msg.strip()

        return True, "Extraction successful"

    except FileNotFoundError as e:
        tool_map = {
            'zip': 'unzip',
            'tar.gz': 'tar',
            'tar': 'tar',
            'rar': 'unrar (brew install unrar / apt install unrar)',
            '7z': '7z (brew install p7zip / apt install p7zip-full)'
        }
        tool = tool_map.get(fmt, fmt)
        return False, f"Extraction tool not installed: {tool}"
    except Exception as e:
        return False, str(e)


def list_files(directory: str) -> list:
    """Recursively list all files in the directory"""
    files = []
    base_path = Path(directory)

    for item in base_path.rglob('*'):
        if item.is_file():
            # Return the path relative to the extraction directory
            rel_path = str(item.relative_to(base_path))
            files.append(rel_path)

    files.sort()
    return files


def main():
    if len(sys.argv) < 2:
        print(json.dumps({
            "success": False,
            "error": "Usage: unzip.py <archive_path>"
        }, ensure_ascii=False))
        sys.exit(1)

    archive_path = sys.argv[1]

    # Check if the file exists
    if not os.path.exists(archive_path):
        print(json.dumps({
            "success": False,
            "error": f"File not found: {archive_path}"
        }, ensure_ascii=False))
        sys.exit(1)

    # Detect format
    fmt = detect_format(archive_path)
    if fmt == 'unknown':
        print(json.dumps({
            "success": False,
            "error": f"Unsupported archive format. Supported formats: zip, tar, tar.gz, rar, 7z"
        }, ensure_ascii=False))
        sys.exit(1)

    # Generate the output directory
    output_dir = get_output_dir(archive_path)

    # Perform extraction
    success, message = unzip_file(archive_path, output_dir, fmt)

    if not success:
        print(json.dumps({
            "success": False,
            "error": message
        }, ensure_ascii=False))
        sys.exit(1)

    # List the extracted files
    files = list_files(output_dir)

    # Output the result
    result = {
        "success": True,
        "source": archive_path,
        "format": fmt,
        "output_dir": output_dir,
        "file_count": len(files),
        "files": files
    }

    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
