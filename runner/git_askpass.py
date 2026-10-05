#!/usr/bin/env python3
"""只向 Git 凭据管道输出当前仓库的短期凭据，不写日志。"""
import os
import sys
print("oauth2" if "username" in sys.argv[-1].lower() else os.environ.get("ALERTOPS_GIT_TOKEN", ""))
