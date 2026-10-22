"""Calls of the manage/write handles; only run inside the killable worker processes.

Reads (also the previous state before a write) use the readonly credentials; a write
asks for its own credential object with exactly one write scope (SEC W1). No retries,
no raw Google text. Annotations and channel groups exist only in the Admin API v1alpha:
a record with a field this adapter does not know stops before anything is written (H3).
"""

from googleapiclient.errors import HttpError
from .domain import ReportError
from .manage import (
    ManageRequest,
    annotation_body,
    channel_group_body,
    channel_group_rules,
    envelope,
    project_annotation,
    project_group,
    project_inspection,
    project_sitemap,
    shape_error,
    trailing_id,
)

LIST_CAP = 100
PAGE_SIZE = 200


def missing(exc):
    return isinstance(exc, HttpError) and getattr(exc.resp, "status", None) == 404


class ManageRunner:
    def __init__(self, cfg, adapter):
        self.cfg = cfg
        self.adapter = adapter
        self.parent = "properties/" + cfg.ga4_property

    def run(self, req: ManageRequest):
        return getattr(self, "op_" + req.operation)(req)

    # --- shared ---------------------------------------------------------------

    def listing(self, req, items, truncated=False):
        quality = {"truncated": truncated, "returned_items": len(items)}
        return envelope(
            req.source,
            req.operation,
            "ok" if items else "no_rows",
            items=items,
            quality=quality,
        )

    def result(self, req, state, target, before, after, changed):
        return envelope(
            req.source,
            req.operation,
            state,
            dry_run=req.dry_run,
            target=target,
            changed_fields=changed,
            before=before,
            after=after,
        )

    def write_state(self, req):
        return "preview" if req.dry_run else "applied"

    def annotations(self, scope_edit=False):
        service = self.adapter.ga_edit() if scope_edit else self.adapter.ga_read()
        return service.properties().reportingDataAnnotations()

    def groups(self, scope_edit=False):
        service = self.adapter.ga_edit() if scope_edit else self.adapter.ga_read()
        return service.properties().channelGroups()

    # --- Search Console ---------------------------------------------------------

    def op_list_sitemaps(self, req):
        raw = self.adapter.gsc_read().sitemaps().list(siteUrl=self.cfg.gsc_property).execute(num_retries=0)
        entries = raw.get("sitemap", []) if isinstance(raw, dict) else None
        if not isinstance(entries, list):
            raise shape_error()
        items = [project_sitemap(e) for e in entries[:LIST_CAP]]
        return self.listing(req, items, len(entries) > LIST_CAP)

    def op_get_sitemap(self, req):
        try:
            raw = (
                self.adapter.gsc_read()
                .sitemaps()
                .get(siteUrl=self.cfg.gsc_property, feedpath=req.params["sitemap_url"])
                .execute(num_retries=0)
            )
        except HttpError as exc:
            if missing(exc):
                raise ReportError(404, "sitemap_not_found") from None
            raise
        return self.listing(req, [project_sitemap(raw)])

    def op_inspect_url(self, req):
        body = {
            "inspectionUrl": req.params["url"],
            "siteUrl": self.cfg.gsc_property,
            "languageCode": "en-US",
        }
        raw = self.adapter.gsc_read().urlInspection().index().inspect(body=body).execute(num_retries=0)
        item = project_inspection(raw, req.params["page_path"], req.params["url"])
        return self.listing(req, [item])

    def sitemap_state(self, url):
        try:
            raw = (
                self.adapter.gsc_read()
                .sitemaps()
                .get(siteUrl=self.cfg.gsc_property, feedpath=url)
                .execute(num_retries=0)
            )
        except HttpError as exc:
            if missing(exc):
                return {"registered": False}
            raise
        state = {"registered": True}
        submitted = project_sitemap(raw)["last_submitted"]
        if submitted:
            state["last_submitted"] = submitted
        return state

    def op_submit_sitemap(self, req):
        url = req.params["sitemap_url"]
        before = self.sitemap_state(url)
        if not req.dry_run:
            self.adapter.gsc_write().sitemaps().submit(
                siteUrl=self.cfg.gsc_property, feedpath=url
            ).execute(num_retries=0)
        after = {"registered": True, "sitemap_url": url}
        return self.result(req, self.write_state(req), url, before, after, ["registered"])

    # --- GA4 annotations ----------------------------------------------------------

    def op_list_annotations(self, req):
        raw = self.annotations().list(parent=self.parent, pageSize=PAGE_SIZE).execute(num_retries=0)
        if not isinstance(raw, dict):
            raise shape_error()
        entries = raw.get("reportingDataAnnotations", [])
        if not isinstance(entries, list):
            raise shape_error()
        items = [project_annotation(e, self.parent, False) for e in entries]
        return self.listing(req, items, bool(raw.get("nextPageToken")))

    def op_create_annotation(self, req):
        params = req.params
        after = {
            "annotation_date": params["date"],
            "color": params["color"],
            "title": params["title"],
        }
        if "description" in params:
            after["description"] = params["description"]
        target = None
        if not req.dry_run:
            created = (
                self.annotations(True)
                .create(parent=self.parent, body=annotation_body(params))
                .execute(num_retries=0)
            )
            if isinstance(created, dict):
                target = trailing_id(created.get("name"), self.parent + "/reportingDataAnnotations")
        return self.result(req, self.write_state(req), target, None, after, sorted(after))

    def op_update_annotation(self, req):
        params = req.params
        name = self.parent + "/reportingDataAnnotations/" + params["annotation_id"]
        try:
            raw = self.annotations().get(name=name).execute(num_retries=0)
        except HttpError as exc:
            if missing(exc):
                raise ReportError(404, "annotation_not_found") from None
            raise
        current = project_annotation(raw, self.parent, True)
        if not current["managed"]:
            raise ReportError(409, "annotation_not_managed")
        before = {
            "annotation_date": current["date"],
            "color": current["color"],
            "title": current["title"],
        }
        if current["description"] is not None:
            before["description"] = current["description"]
        wanted = {
            "annotation_date": params.get("date"),
            "color": params.get("color"),
            "title": params.get("title"),
            "description": params.get("description"),
        }
        wanted = {k: v for k, v in wanted.items() if v is not None}
        after = dict(before, **wanted)
        changed = sorted(k for k in wanted if before.get(k) != after[k])
        if not changed:
            return self.result(req, "unchanged", params["annotation_id"], before, after, [])
        if not req.dry_run:
            fields = {k: after[k] for k in changed}
            body = annotation_body(
                {("date" if k == "annotation_date" else k): v for k, v in fields.items()}
            )
            self.annotations(True).patch(
                name=name, updateMask=",".join(changed), body=body
            ).execute(num_retries=0)
        return self.result(
            req, self.write_state(req), params["annotation_id"], before, after, changed
        )

    # --- GA4 channel groups ----------------------------------------------------------

    def op_list_channel_groups(self, req):
        raw = self.groups().list(parent=self.parent, pageSize=PAGE_SIZE).execute(num_retries=0)
        if not isinstance(raw, dict) or not isinstance(raw.get("channelGroups", []), list):
            raise shape_error()
        items = [project_group(self.cfg, g, self.parent, False) for g in raw.get("channelGroups", [])]
        return self.listing(req, items, bool(raw.get("nextPageToken")))

    def op_create_channel_group(self, req):
        hosts = req.params["source_hosts"]
        listed = self.op_list_channel_groups(req)["items"]
        if any(g["managed"] for g in listed):
            raise ReportError(409, "channel_group_exists")
        body = channel_group_body(self.cfg, hosts)
        after = {
            "channel_group_name": self.cfg.ga4_channel_group_name,
            "source_hosts": hosts,
            "rule_count": len(body["groupingRule"]),
            "primary": False,
        }
        target = None
        if not req.dry_run:
            created = self.groups(True).create(parent=self.parent, body=body).execute(num_retries=0)
            if isinstance(created, dict):
                target = trailing_id(created.get("name"), self.parent + "/channelGroups")
        return self.result(req, self.write_state(req), target, None, after, sorted(after))

    def op_update_channel_group(self, req):
        params = req.params
        name = self.parent + "/channelGroups/" + params["channel_group_id"]
        try:
            raw = self.groups().get(name=name).execute(num_retries=0)
        except HttpError as exc:
            if missing(exc):
                raise ReportError(404, "channel_group_not_found") from None
            raise
        current = project_group(self.cfg, raw, self.parent, True)
        if not current["managed"]:
            raise ReportError(409, "channel_group_not_managed")
        rules = channel_group_rules(self.cfg, params["source_hosts"])
        before = {
            "channel_group_name": current["display_name"],
            "source_hosts": current["source_hosts"],
            "rule_count": current["rule_count"],
            "primary": current["primary"],
        }
        after = dict(before, source_hosts=params["source_hosts"], rule_count=len(rules))
        if raw.get("groupingRule") == rules:
            return self.result(req, "unchanged", params["channel_group_id"], before, after, [])
        changed = [k for k in ("source_hosts", "rule_count") if before[k] != after[k]] or [
            "grouping_rule"
        ]
        if not req.dry_run:
            # Only the rules: neither the name nor the primary flag is ever part of a patch.
            self.groups(True).patch(
                name=name, updateMask="grouping_rule", body={"groupingRule": rules}
            ).execute(num_retries=0)
        return self.result(
            req, self.write_state(req), params["channel_group_id"], before, after, changed
        )
