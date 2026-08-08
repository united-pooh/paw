"""支持 `python -m quant_mcp` 直接以 stdio 启动。"""

import argparse

from .server import build


def main() -> None:
    parser = argparse.ArgumentParser(description="quant-mcp MCP 服务器")
    parser.add_argument("--transport", choices=["stdio", "sse"], default="stdio",
                        help="传输方式（默认 stdio）")
    args = parser.parse_args()
    mcp = build()
    if args.transport == "sse":
        mcp.settings.port = 8899
        mcp.run(transport="sse")
    else:
        mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
