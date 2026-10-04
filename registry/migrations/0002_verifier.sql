-- LLM verifier: spot-checks decision-model answers. It never changes a decision.
ALTER TABLE decisions ADD COLUMN state TEXT;
ALTER TABLE decisions ADD COLUMN verify_model TEXT;
ALTER TABLE decisions ADD COLUMN verify_agree INTEGER;
ALTER TABLE decisions ADD COLUMN verify_answer TEXT;
ALTER TABLE decisions ADD COLUMN verify_reason TEXT;
ALTER TABLE decisions ADD COLUMN verified_at TEXT;
CREATE INDEX decisions_verify ON decisions(verify_model, verify_agree);
