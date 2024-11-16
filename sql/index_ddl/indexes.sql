CREATE INDEX reactions_livestream_id_idx ON reactions(`livestream_id`);
CREATE INDEX reservation_slots_between_start_end ON reservation_slots(`start_at`, `end_at`);
CREATE INDEX livecomment_reports_livestream_id_idx ON livecomment_reports(`livestream_id`);
CREATE INDEX livestream_tags_livestream_id_idx ON livestream_tags(`livestream_id`);
CREATE INDEX ng_words_user_id_and_livestream_id_idx ON ng_words(`user_id`, `livestream_id`);
CREATE INDEX icons_user_id_idx ON icons(`user_id`);
CREATE INDEX livestreams_user_id_idx ON livestreams(`user_id`);
