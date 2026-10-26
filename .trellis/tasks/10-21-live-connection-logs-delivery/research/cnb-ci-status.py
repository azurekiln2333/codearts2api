"""Read public CNB SSR build state without credentials or API requests."""
import argparse
import json
import re
from urllib.request import Request, urlopen


def fetch_page(url):
    req = Request(url, headers={"User-Agent": "codearts2api-public-ci-status"})
    with urlopen(req, timeout=25) as response:
        html = response.read().decode("utf-8")
    match = re.search(r'<script id="__NEXT_DATA__"[^>]*>(.*?)</script>', html, re.S)
    if not match:
        raise RuntimeError("CNB page has no public SSR build data")
    return json.loads(match.group(1))["props"]["pageProps"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default="azurekiln/codearts2api")
    parser.add_argument("--sn", help="Specific run; defaults to latest matching run")
    parser.add_argument("--sha", help="Optional commit prefix filter for latest listing")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+", args.repo):
        parser.error("Invalid repository path")
    base = "https://cnb.cool/" + args.repo + "/-/build/logs"
    if args.sn:
        sn = args.sn
    else:
        listing = fetch_page(base).get("pipelines", [])
        if args.sha:
            listing = [x for x in listing if x.get("sha", "").startswith(args.sha)]
        if not listing:
            print(json.dumps({"found": False, "repo": args.repo, "sha_filter": args.sha}))
            return
        sn = listing[0]["sn"]
    if not re.fullmatch(r"cnb-[A-Za-z0-9-]+", sn):
        parser.error("Invalid build ID")
    meta = fetch_page(base + "/" + sn)["metaData"]
    result = {k: meta.get(k) for k in ("sn", "sha", "sourceRef", "targetRef", "event", "status", "duration")}
    result["url"] = base + "/" + sn
    result["pipelines"] = []
    for pipeline in meta.get("pipelines", {}).values():
        item = {k: pipeline.get(k) for k in ("id", "name", "status", "duration")}
        item["stages"] = []
        for stage in pipeline.get("stages", []):
            stage_item = {k: stage.get(k) for k in ("name", "status")}
            errors = []
            for job in stage.get("jobs", []):
                if job.get("status") == "error":
                    text = job.get("errMsg") or (job.get("result") or {}).get("stderr") or stage.get("errMsg") or "No public error text"
                    errors.append({"job": job.get("name"), "error": text[-12000:]})
            if errors:
                stage_item["errors"] = errors
            item["stages"].append(stage_item)
        result["pipelines"].append(item)
    print(json.dumps(result, ensure_ascii=True, indent=2))


if __name__ == "__main__":
    main()
