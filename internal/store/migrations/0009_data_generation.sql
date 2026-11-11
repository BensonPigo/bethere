-- The data generation (ADR-0041): one counter that every insert, update
-- and delete of any table raises, through triggers, whoever writes:
-- the service, its background work or a command on the host. A poll
-- uses the event data it keeps in memory only while the counter and the
-- schema version are what they were when it was read. Every new table,
-- and every rebuilt one after its rename, gets the same three triggers;
-- a test lists the tables to make sure.
CREATE TABLE data_generation (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  n  INTEGER NOT NULL
);
INSERT INTO data_generation (id, n) VALUES (1, 0);

CREATE TRIGGER organizations_gen_ins AFTER INSERT ON organizations BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER organizations_gen_upd AFTER UPDATE ON organizations BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER organizations_gen_del AFTER DELETE ON organizations BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER events_gen_ins AFTER INSERT ON events BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER events_gen_upd AFTER UPDATE ON events BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER events_gen_del AFTER DELETE ON events BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER roles_gen_ins AFTER INSERT ON roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER roles_gen_upd AFTER UPDATE ON roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER roles_gen_del AFTER DELETE ON roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER access_tokens_gen_ins AFTER INSERT ON access_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER access_tokens_gen_upd AFTER UPDATE ON access_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER access_tokens_gen_del AFTER DELETE ON access_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cues_gen_ins AFTER INSERT ON cues BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cues_gen_upd AFTER UPDATE ON cues BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cues_gen_del AFTER DELETE ON cues BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_roles_gen_ins AFTER INSERT ON cue_roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_roles_gen_upd AFTER UPDATE ON cue_roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_roles_gen_del AFTER DELETE ON cue_roles BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER check_ins_gen_ins AFTER INSERT ON check_ins BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER check_ins_gen_upd AFTER UPDATE ON check_ins BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER check_ins_gen_del AFTER DELETE ON check_ins BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER action_logs_gen_ins AFTER INSERT ON action_logs BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER action_logs_gen_upd AFTER UPDATE ON action_logs BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER action_logs_gen_del AFTER DELETE ON action_logs BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_acks_gen_ins AFTER INSERT ON cue_acks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_acks_gen_upd AFTER UPDATE ON cue_acks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER cue_acks_gen_del AFTER DELETE ON cue_acks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER tasks_gen_ins AFTER INSERT ON tasks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER tasks_gen_upd AFTER UPDATE ON tasks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER tasks_gen_del AFTER DELETE ON tasks BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER guests_gen_ins AFTER INSERT ON guests BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER guests_gen_upd AFTER UPDATE ON guests BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER guests_gen_del AFTER DELETE ON guests BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER checklist_items_gen_ins AFTER INSERT ON checklist_items BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER checklist_items_gen_upd AFTER UPDATE ON checklist_items BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER checklist_items_gen_del AFTER DELETE ON checklist_items BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER invites_gen_ins AFTER INSERT ON invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER invites_gen_upd AFTER UPDATE ON invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER invites_gen_del AFTER DELETE ON invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_tokens_gen_ins AFTER INSERT ON admin_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_tokens_gen_upd AFTER UPDATE ON admin_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_tokens_gen_del AFTER DELETE ON admin_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_invites_gen_ins AFTER INSERT ON admin_invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_invites_gen_upd AFTER UPDATE ON admin_invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER admin_invites_gen_del AFTER DELETE ON admin_invites BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER members_gen_ins AFTER INSERT ON members BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER members_gen_upd AFTER UPDATE ON members BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER members_gen_del AFTER DELETE ON members BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER member_sessions_gen_ins AFTER INSERT ON member_sessions BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER member_sessions_gen_upd AFTER UPDATE ON member_sessions BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER member_sessions_gen_del AFTER DELETE ON member_sessions BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER console_tokens_gen_ins AFTER INSERT ON console_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER console_tokens_gen_upd AFTER UPDATE ON console_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER console_tokens_gen_del AFTER DELETE ON console_tokens BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER login_mails_gen_ins AFTER INSERT ON login_mails BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER login_mails_gen_upd AFTER UPDATE ON login_mails BEGIN UPDATE data_generation SET n = n + 1; END;
CREATE TRIGGER login_mails_gen_del AFTER DELETE ON login_mails BEGIN UPDATE data_generation SET n = n + 1; END;

-- The change notices look back through one event's shifts and inserts
-- (loadShifts, loadAssigns) among its many check-ins and acks.
CREATE INDEX action_logs_event_type ON action_logs (event_id, type, id);
