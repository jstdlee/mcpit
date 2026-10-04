-- Device keys are approved automatically; the moderator can still revoke them.
INSERT OR IGNORE INTO settings (key, value) VALUES ('auto_approve_keys', 'true');
INSERT OR IGNORE INTO settings (key, value) VALUES ('keys_per_network_per_day', '5');
