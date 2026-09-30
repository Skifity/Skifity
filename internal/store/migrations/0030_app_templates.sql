-- Which template an app was installed from, so the panel can offer the
-- template's newer version when an upgrade of the panel brings one.
--
-- installed_image is the image the template set, which is what "changed by
-- hand" is measured against: an app whose image somebody has since changed is
-- not updated over their head. update_status is '' at rest, 'backing_up' while
-- the backups an update takes first are running, and 'failed' with the
-- reason when one of them did not finish, in which case nothing was changed.
CREATE TABLE app_templates (
    app_id          TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    template_id     TEXT NOT NULL,
    service         TEXT NOT NULL,
    installed_image TEXT NOT NULL,
    installed_at    TEXT NOT NULL,
    update_status   TEXT NOT NULL DEFAULT '',
    update_to       TEXT NOT NULL DEFAULT '',
    update_error    TEXT NOT NULL DEFAULT ''
);
