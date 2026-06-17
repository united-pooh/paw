#!/bin/bash

# 后台任务脚本：每2秒输出一次"后台任务运行中..."，持续30秒
END=$((SECONDS + 30))

while [ $SECONDS -lt $END ]; do
    echo "后台任务运行中..."
    sleep 2
done

echo "后台任务已结束。"
