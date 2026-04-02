package sqlite

// migrationsEvents contains schema for events, groups, attachments, debug
// files, release sessions, transactions, spans, issue activity, ownership,
// release deploys/commits, profiles, native crashes, and replays.
var migrationsEvents = []schemaMigration{
	{2, `
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			group_id TEXT,
			release TEXT,
			environment TEXT,
			platform TEXT,
			level TEXT,
			title TEXT,
			culprit TEXT,
			message TEXT,
			tags_json TEXT DEFAULT '{}',
			payload_json TEXT,
			occurred_at TEXT,
			ingested_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, event_id)
		);
		CREATE INDEX idx_events_project_time ON events(project_id, ingested_at DESC);
		CREATE INDEX idx_events_group ON events(group_id, ingested_at DESC);

		CREATE TABLE groups (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			grouping_version TEXT NOT NULL,
			grouping_key TEXT NOT NULL,
			title TEXT,
			culprit TEXT,
			level TEXT,
			status TEXT DEFAULT 'unresolved',
			first_seen TEXT,
			last_seen TEXT,
			times_seen INTEGER DEFAULT 0,
			last_event_id TEXT,
			UNIQUE(project_id, grouping_version, grouping_key)
		);
		CREATE INDEX idx_groups_project_status ON groups(project_id, status, last_seen DESC);
	`},
	{5, `
		ALTER TABLE events ADD COLUMN user_identifier TEXT;
		CREATE INDEX idx_events_user ON events(project_id, user_identifier);

		CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			release_version TEXT NOT NULL,
			name TEXT NOT NULL,
			object_key TEXT NOT NULL,
			size INTEGER DEFAULT 0,
			checksum TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, release_version, name)
		);
		CREATE INDEX IF NOT EXISTS idx_artifacts_lookup ON artifacts(project_id, release_version, name);
	`},
	{6, `
		ALTER TABLE groups ADD COLUMN assignee TEXT;
		ALTER TABLE groups ADD COLUMN priority INTEGER DEFAULT 2;
	`},
	{8, `
		ALTER TABLE groups ADD COLUMN short_id INTEGER;
	`},
	{9, `
		CREATE INDEX IF NOT EXISTS idx_events_environment ON events(environment);
		CREATE INDEX IF NOT EXISTS idx_groups_priority ON groups(priority);
		CREATE INDEX IF NOT EXISTS idx_groups_first_seen ON groups(first_seen);
		CREATE INDEX IF NOT EXISTS idx_groups_times_seen ON groups(times_seen);
	`},
	{11, `
		CREATE TABLE event_attachments (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			event_id TEXT NOT NULL,
			name TEXT NOT NULL,
			content_type TEXT,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			object_key TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_event_attachments_event ON event_attachments(event_id, created_at DESC);
		CREATE INDEX idx_event_attachments_project ON event_attachments(project_id, created_at DESC);
	`},
	{12, `
		CREATE TABLE debug_files (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			release_version TEXT NOT NULL,
			uuid TEXT NOT NULL,
			code_id TEXT,
			name TEXT NOT NULL,
			object_key TEXT NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			checksum TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX idx_debug_files_release ON debug_files(project_id, release_version, created_at DESC);
		CREATE INDEX idx_debug_files_uuid ON debug_files(project_id, uuid, created_at DESC);
	`},
	{15, `
		ALTER TABLE projects ADD COLUMN event_retention_days INTEGER NOT NULL DEFAULT 90;
		ALTER TABLE projects ADD COLUMN attachment_retention_days INTEGER NOT NULL DEFAULT 30;
		ALTER TABLE projects ADD COLUMN debug_file_retention_days INTEGER NOT NULL DEFAULT 180;
		ALTER TABLE debug_files ADD COLUMN kind TEXT NOT NULL DEFAULT 'proguard';
		ALTER TABLE debug_files ADD COLUMN content_type TEXT;
		CREATE TABLE IF NOT EXISTS release_sessions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			release_version TEXT NOT NULL,
			environment TEXT DEFAULT '',
			session_id TEXT,
			distinct_id TEXT,
			status TEXT NOT NULL DEFAULT 'ok',
			errors INTEGER NOT NULL DEFAULT 0,
			started_at TEXT,
			duration REAL NOT NULL DEFAULT 0,
			user_agent TEXT,
			attrs_json TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_release_sessions_project_release ON release_sessions(project_id, release_version, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_release_sessions_status ON release_sessions(project_id, release_version, status, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_release_sessions_distinct ON release_sessions(project_id, release_version, distinct_id);
	`},
	{17, `
		ALTER TABLE events ADD COLUMN payload_key TEXT;
	`},
	{18, `
		ALTER TABLE release_sessions ADD COLUMN quantity INTEGER NOT NULL DEFAULT 1;
	`},
	{19, `
		CREATE TABLE IF NOT EXISTS outcomes (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			event_id TEXT,
			category TEXT NOT NULL,
			reason TEXT NOT NULL,
			quantity INTEGER NOT NULL DEFAULT 1,
			source TEXT NOT NULL DEFAULT 'client_report',
			release TEXT,
			environment TEXT,
			payload_json TEXT DEFAULT '{}',
			recorded_at TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_outcomes_project_recorded ON outcomes(project_id, recorded_at DESC);
		CREATE INDEX IF NOT EXISTS idx_outcomes_project_category ON outcomes(project_id, category, recorded_at DESC);

		CREATE TABLE IF NOT EXISTS monitors (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			slug TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			environment TEXT DEFAULT '',
			schedule_type TEXT,
			schedule_value INTEGER,
			schedule_unit TEXT,
			schedule_crontab TEXT,
			checkin_margin INTEGER NOT NULL DEFAULT 0,
			max_runtime INTEGER NOT NULL DEFAULT 0,
			timezone TEXT DEFAULT 'UTC',
			config_json TEXT DEFAULT '{}',
			last_checkin_id TEXT,
			last_status TEXT,
			last_checkin_at TEXT,
			next_checkin_at TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, slug)
		);
		CREATE INDEX IF NOT EXISTS idx_monitors_project_updated ON monitors(project_id, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_monitors_due ON monitors(status, next_checkin_at);

		CREATE TABLE IF NOT EXISTS monitor_checkins (
			id TEXT PRIMARY KEY,
			monitor_id TEXT NOT NULL REFERENCES monitors(id),
			project_id TEXT NOT NULL REFERENCES projects(id),
			check_in_id TEXT NOT NULL,
			monitor_slug TEXT NOT NULL,
			status TEXT NOT NULL,
			duration REAL NOT NULL DEFAULT 0,
			release TEXT,
			environment TEXT DEFAULT '',
			scheduled_for TEXT,
			payload_json TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, check_in_id)
		);
		CREATE INDEX IF NOT EXISTS idx_monitor_checkins_monitor_created ON monitor_checkins(monitor_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_monitor_checkins_project_created ON monitor_checkins(project_id, created_at DESC);
	`},
	{20, `
		CREATE TABLE IF NOT EXISTS transactions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			event_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			span_id TEXT NOT NULL,
			parent_span_id TEXT,
			transaction_name TEXT NOT NULL,
			op TEXT,
			status TEXT,
			platform TEXT,
			environment TEXT,
			release TEXT,
			start_timestamp TEXT NOT NULL,
			end_timestamp TEXT NOT NULL,
			duration_ms REAL NOT NULL,
			tags_json TEXT DEFAULT '{}',
			measurements_json TEXT DEFAULT '{}',
			payload_json TEXT,
			payload_key TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, event_id)
		);
		CREATE INDEX IF NOT EXISTS idx_transactions_project_created ON transactions(project_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_transactions_project_trace ON transactions(project_id, trace_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_transactions_project_name ON transactions(project_id, transaction_name, created_at DESC);

		CREATE TABLE IF NOT EXISTS spans (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			transaction_event_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			span_id TEXT NOT NULL,
			parent_span_id TEXT,
			op TEXT,
			description TEXT,
			status TEXT,
			start_timestamp TEXT NOT NULL,
			end_timestamp TEXT NOT NULL,
			duration_ms REAL NOT NULL,
			tags_json TEXT DEFAULT '{}',
			data_json TEXT DEFAULT '{}',
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(project_id, trace_id, span_id)
		);
		CREATE INDEX IF NOT EXISTS idx_spans_project_trace_start ON spans(project_id, trace_id, start_timestamp ASC);
	`},
	{21, `
		ALTER TABLE events ADD COLUMN event_type TEXT NOT NULL DEFAULT 'error';
		CREATE INDEX IF NOT EXISTS idx_events_project_event_type_ingested ON events(project_id, event_type, ingested_at DESC);
	`},
	{22, `
		ALTER TABLE groups ADD COLUMN resolution_substatus TEXT DEFAULT '';
		ALTER TABLE groups ADD COLUMN resolved_in_release TEXT DEFAULT '';
		ALTER TABLE groups ADD COLUMN resolved_at TEXT;
		ALTER TABLE groups ADD COLUMN resolved_by_user_id TEXT;
		ALTER TABLE groups ADD COLUMN merged_into_group_id TEXT;
		ALTER TABLE groups ADD COLUMN merged_at TEXT;
		ALTER TABLE groups ADD COLUMN merged_by_user_id TEXT;
		CREATE TABLE IF NOT EXISTS issue_comments (
			id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL REFERENCES groups(id),
			project_id TEXT NOT NULL REFERENCES projects(id),
			user_id TEXT REFERENCES users(id),
			body TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_issue_comments_group ON issue_comments(group_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_issue_comments_project ON issue_comments(project_id, created_at DESC);
		CREATE TABLE IF NOT EXISTS issue_activity (
			id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL REFERENCES groups(id),
			project_id TEXT NOT NULL REFERENCES projects(id),
			user_id TEXT REFERENCES users(id),
			kind TEXT NOT NULL,
			summary TEXT NOT NULL,
			details TEXT DEFAULT '',
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_issue_activity_group ON issue_activity(group_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_issue_activity_project ON issue_activity(project_id, created_at DESC);
		CREATE TABLE IF NOT EXISTS issue_bookmarks (
			user_id TEXT NOT NULL REFERENCES users(id),
			group_id TEXT NOT NULL REFERENCES groups(id),
			created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (user_id, group_id)
		);
		CREATE INDEX IF NOT EXISTS idx_issue_bookmarks_group ON issue_bookmarks(group_id, created_at DESC);
		CREATE TABLE IF NOT EXISTS issue_subscriptions (
			user_id TEXT NOT NULL REFERENCES users(id),
			group_id TEXT NOT NULL REFERENCES groups(id),
			created_at TEXT DEFAULT (datetime('now')),
			PRIMARY KEY (user_id, group_id)
		);
		CREATE INDEX IF NOT EXISTS idx_issue_subscriptions_group ON issue_subscriptions(group_id, created_at DESC);
	`},
	{23, `
		CREATE TABLE IF NOT EXISTS ownership_rules (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			name TEXT NOT NULL,
			pattern TEXT NOT NULL,
			assignee TEXT NOT NULL,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_ownership_rules_project ON ownership_rules(project_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS release_deploys (
			id TEXT PRIMARY KEY,
			release_id TEXT NOT NULL REFERENCES releases(id),
			environment TEXT NOT NULL,
			name TEXT DEFAULT '',
			url TEXT DEFAULT '',
			date_started TEXT,
			date_finished TEXT,
			created_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_release_deploys_release ON release_deploys(release_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS release_commits (
			id TEXT PRIMARY KEY,
			release_id TEXT NOT NULL REFERENCES releases(id),
			commit_sha TEXT NOT NULL,
			repository TEXT DEFAULT '',
			author_name TEXT DEFAULT '',
			author_email TEXT DEFAULT '',
			message TEXT DEFAULT '',
			files_json TEXT DEFAULT '[]',
			created_at TEXT DEFAULT (datetime('now')),
			UNIQUE(release_id, commit_sha)
		);
		CREATE INDEX IF NOT EXISTS idx_release_commits_release ON release_commits(release_id, created_at DESC);
	`},
	{29, `
			CREATE TABLE IF NOT EXISTS profile_manifests (
				id TEXT PRIMARY KEY,
				event_row_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,
				project_id TEXT NOT NULL REFERENCES projects(id),
				event_id TEXT NOT NULL DEFAULT '',
				profile_id TEXT NOT NULL,
				trace_id TEXT DEFAULT '',
				transaction_name TEXT DEFAULT '',
				release TEXT DEFAULT '',
				environment TEXT DEFAULT '',
				platform TEXT DEFAULT '',
				profile_kind TEXT DEFAULT 'sampled',
				started_at TEXT,
				ended_at TEXT,
				duration_ns INTEGER NOT NULL DEFAULT 0,
				thread_count INTEGER NOT NULL DEFAULT 0,
				sample_count INTEGER NOT NULL DEFAULT 0,
				frame_count INTEGER NOT NULL DEFAULT 0,
				function_count INTEGER NOT NULL DEFAULT 0,
				stack_count INTEGER NOT NULL DEFAULT 0,
				processing_status TEXT NOT NULL DEFAULT 'completed',
				ingest_error TEXT DEFAULT '',
				raw_blob_key TEXT DEFAULT '',
				created_at TEXT DEFAULT (datetime('now')),
				updated_at TEXT DEFAULT (datetime('now')),
				UNIQUE(project_id, profile_id)
			);
			CREATE INDEX IF NOT EXISTS idx_profile_manifests_project_started
				ON profile_manifests(project_id, started_at DESC, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_profile_manifests_project_trace
				ON profile_manifests(project_id, trace_id, started_at DESC);
			CREATE INDEX IF NOT EXISTS idx_profile_manifests_status
				ON profile_manifests(project_id, processing_status, created_at DESC);

			CREATE TABLE IF NOT EXISTS profile_threads (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES profile_manifests(id) ON DELETE CASCADE,
				thread_key TEXT NOT NULL,
				thread_name TEXT DEFAULT '',
				thread_role TEXT DEFAULT 'unknown',
				is_main INTEGER NOT NULL DEFAULT 0,
				sample_count INTEGER NOT NULL DEFAULT 0,
				duration_ns INTEGER NOT NULL DEFAULT 0,
				UNIQUE(manifest_id, thread_key)
			);
			CREATE INDEX IF NOT EXISTS idx_profile_threads_manifest
				ON profile_threads(manifest_id, thread_key);

			CREATE TABLE IF NOT EXISTS profile_frames (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES profile_manifests(id) ON DELETE CASCADE,
				frame_key TEXT NOT NULL,
				frame_label TEXT NOT NULL,
				function_label TEXT NOT NULL,
				function_name TEXT DEFAULT '',
				module_name TEXT DEFAULT '',
				package_name TEXT DEFAULT '',
				filename TEXT DEFAULT '',
				lineno INTEGER NOT NULL DEFAULT 0,
				in_app INTEGER NOT NULL DEFAULT 0,
				image_ref TEXT DEFAULT '',
				UNIQUE(manifest_id, frame_key)
			);
			CREATE INDEX IF NOT EXISTS idx_profile_frames_manifest
				ON profile_frames(manifest_id, frame_label);
			CREATE INDEX IF NOT EXISTS idx_profile_frames_function
				ON profile_frames(manifest_id, function_label);

			CREATE TABLE IF NOT EXISTS profile_stacks (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES profile_manifests(id) ON DELETE CASCADE,
				stack_key TEXT NOT NULL,
				leaf_frame_id TEXT NOT NULL REFERENCES profile_frames(id),
				root_frame_id TEXT NOT NULL REFERENCES profile_frames(id),
				depth INTEGER NOT NULL DEFAULT 0,
				UNIQUE(manifest_id, stack_key)
			);
			CREATE INDEX IF NOT EXISTS idx_profile_stacks_manifest
				ON profile_stacks(manifest_id, depth DESC);

			CREATE TABLE IF NOT EXISTS profile_stack_frames (
				manifest_id TEXT NOT NULL REFERENCES profile_manifests(id) ON DELETE CASCADE,
				stack_id TEXT NOT NULL REFERENCES profile_stacks(id) ON DELETE CASCADE,
				position INTEGER NOT NULL,
				frame_id TEXT NOT NULL REFERENCES profile_frames(id),
				PRIMARY KEY (stack_id, position)
			);
			CREATE INDEX IF NOT EXISTS idx_profile_stack_frames_manifest
				ON profile_stack_frames(manifest_id, stack_id, position);

			CREATE TABLE IF NOT EXISTS profile_samples (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES profile_manifests(id) ON DELETE CASCADE,
				thread_row_id TEXT NOT NULL REFERENCES profile_threads(id),
				stack_id TEXT NOT NULL REFERENCES profile_stacks(id),
				ts_ns INTEGER,
				weight INTEGER NOT NULL DEFAULT 1,
				wall_time_ns INTEGER,
				queue_time_ns INTEGER,
				cpu_time_ns INTEGER,
				is_idle INTEGER NOT NULL DEFAULT 0
			);
			CREATE INDEX IF NOT EXISTS idx_profile_samples_manifest
				ON profile_samples(manifest_id, thread_row_id);
			CREATE INDEX IF NOT EXISTS idx_profile_samples_stack
				ON profile_samples(manifest_id, stack_id);
		`},
	{30, `
			CREATE TABLE IF NOT EXISTS native_symbol_sources (
				id TEXT PRIMARY KEY,
				debug_file_id TEXT NOT NULL REFERENCES debug_files(id) ON DELETE CASCADE,
				project_id TEXT NOT NULL REFERENCES projects(id),
				release_version TEXT NOT NULL,
				kind TEXT NOT NULL DEFAULT 'native',
				debug_id TEXT NOT NULL DEFAULT '',
				code_id TEXT NOT NULL DEFAULT '',
				build_id TEXT NOT NULL DEFAULT '',
				uuid TEXT NOT NULL DEFAULT '',
				module_name TEXT NOT NULL DEFAULT '',
				architecture TEXT NOT NULL DEFAULT '',
				platform TEXT NOT NULL DEFAULT '',
				created_at TEXT DEFAULT (datetime('now')),
				UNIQUE(debug_file_id, debug_id, code_id, build_id, uuid, module_name, architecture)
			);
			CREATE INDEX IF NOT EXISTS idx_native_symbol_sources_project_release_debug
				ON native_symbol_sources(project_id, release_version, debug_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_symbol_sources_project_release_code
				ON native_symbol_sources(project_id, release_version, code_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_symbol_sources_project_release_build
				ON native_symbol_sources(project_id, release_version, build_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_symbol_sources_project_release_uuid
				ON native_symbol_sources(project_id, release_version, uuid, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_symbol_sources_project_release_module
				ON native_symbol_sources(project_id, release_version, module_name, architecture, platform, created_at DESC);

			CREATE TABLE IF NOT EXISTS native_crash_images (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL REFERENCES projects(id),
				event_id TEXT NOT NULL,
				position INTEGER NOT NULL DEFAULT 0,
				release_version TEXT NOT NULL DEFAULT '',
				platform TEXT NOT NULL DEFAULT '',
				image_name TEXT NOT NULL DEFAULT '',
				module_name TEXT NOT NULL DEFAULT '',
				debug_id TEXT NOT NULL DEFAULT '',
				code_id TEXT NOT NULL DEFAULT '',
				build_id TEXT NOT NULL DEFAULT '',
				uuid TEXT NOT NULL DEFAULT '',
				architecture TEXT NOT NULL DEFAULT '',
				image_addr TEXT NOT NULL DEFAULT '',
				image_size TEXT NOT NULL DEFAULT '',
				instruction_addr TEXT NOT NULL DEFAULT '',
				source TEXT NOT NULL DEFAULT 'event',
				created_at TEXT DEFAULT (datetime('now')),
				UNIQUE(project_id, event_id, debug_id, code_id, build_id, uuid, module_name, image_addr)
			);
			CREATE INDEX IF NOT EXISTS idx_native_crash_images_event
				ON native_crash_images(project_id, event_id, position ASC, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_crash_images_release_debug
				ON native_crash_images(project_id, release_version, debug_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_crash_images_release_code
				ON native_crash_images(project_id, release_version, code_id, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_crash_images_release_module
				ON native_crash_images(project_id, release_version, module_name, architecture, platform, created_at DESC);
		`},
	{31, `
			ALTER TABLE events ADD COLUMN processing_status TEXT NOT NULL DEFAULT 'completed';
			ALTER TABLE events ADD COLUMN ingest_error TEXT NOT NULL DEFAULT '';
			CREATE INDEX IF NOT EXISTS idx_events_processing
				ON events(project_id, event_type, processing_status, ingested_at DESC);

			CREATE TABLE IF NOT EXISTS native_crashes (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL REFERENCES projects(id),
				event_id TEXT NOT NULL,
				event_row_id TEXT NOT NULL DEFAULT '',
				release_version TEXT NOT NULL DEFAULT '',
				platform TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'pending',
				ingest_error TEXT NOT NULL DEFAULT '',
				payload_json TEXT NOT NULL DEFAULT '',
				raw_attachment_id TEXT NOT NULL DEFAULT '',
				raw_blob_key TEXT NOT NULL DEFAULT '',
				filename TEXT NOT NULL DEFAULT '',
				content_type TEXT NOT NULL DEFAULT '',
				size_bytes INTEGER NOT NULL DEFAULT 0,
				attempts INTEGER NOT NULL DEFAULT 0,
				processed_at TEXT,
				created_at TEXT DEFAULT (datetime('now')),
				updated_at TEXT DEFAULT (datetime('now')),
				UNIQUE(project_id, event_id)
			);
			CREATE INDEX IF NOT EXISTS idx_native_crashes_status
				ON native_crashes(project_id, status, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_native_crashes_event
				ON native_crashes(project_id, event_id);
		`},
	{34, `
			CREATE TABLE IF NOT EXISTS replay_manifests (
				id TEXT PRIMARY KEY,
				event_row_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,
				project_id TEXT NOT NULL REFERENCES projects(id),
				replay_id TEXT NOT NULL,
				platform TEXT NOT NULL DEFAULT '',
				release TEXT NOT NULL DEFAULT '',
				environment TEXT NOT NULL DEFAULT '',
				started_at TEXT,
				ended_at TEXT,
				duration_ms INTEGER NOT NULL DEFAULT 0,
				request_url TEXT NOT NULL DEFAULT '',
				user_ref_json TEXT NOT NULL DEFAULT '{}',
				trace_ids_json TEXT NOT NULL DEFAULT '[]',
				linked_event_ids_json TEXT NOT NULL DEFAULT '[]',
				linked_issue_ids_json TEXT NOT NULL DEFAULT '[]',
				asset_count INTEGER NOT NULL DEFAULT 0,
				console_count INTEGER NOT NULL DEFAULT 0,
				network_count INTEGER NOT NULL DEFAULT 0,
				click_count INTEGER NOT NULL DEFAULT 0,
				navigation_count INTEGER NOT NULL DEFAULT 0,
				error_marker_count INTEGER NOT NULL DEFAULT 0,
				timeline_start_ms INTEGER NOT NULL DEFAULT 0,
				timeline_end_ms INTEGER NOT NULL DEFAULT 0,
				privacy_policy_version TEXT NOT NULL DEFAULT '',
				processing_status TEXT NOT NULL DEFAULT 'partial',
				ingest_error TEXT NOT NULL DEFAULT '',
				created_at TEXT DEFAULT (datetime('now')),
				updated_at TEXT DEFAULT (datetime('now')),
				UNIQUE(project_id, replay_id)
			);
			CREATE INDEX IF NOT EXISTS idx_replay_manifests_project_started
				ON replay_manifests(project_id, started_at DESC, created_at DESC);
			CREATE INDEX IF NOT EXISTS idx_replay_manifests_project_status
				ON replay_manifests(project_id, processing_status, created_at DESC);

			CREATE TABLE IF NOT EXISTS replay_assets (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES replay_manifests(id) ON DELETE CASCADE,
				replay_id TEXT NOT NULL,
				attachment_id TEXT NOT NULL REFERENCES event_attachments(id) ON DELETE CASCADE,
				kind TEXT NOT NULL,
				name TEXT NOT NULL DEFAULT '',
				content_type TEXT NOT NULL DEFAULT '',
				size_bytes INTEGER NOT NULL DEFAULT 0,
				object_key TEXT NOT NULL DEFAULT '',
				chunk_index INTEGER NOT NULL DEFAULT 0,
				created_at TEXT DEFAULT (datetime('now')),
				UNIQUE(manifest_id, attachment_id)
			);
			CREATE INDEX IF NOT EXISTS idx_replay_assets_manifest_kind
				ON replay_assets(manifest_id, kind, chunk_index, created_at);

			CREATE TABLE IF NOT EXISTS replay_timeline_items (
				id TEXT PRIMARY KEY,
				manifest_id TEXT NOT NULL REFERENCES replay_manifests(id) ON DELETE CASCADE,
				replay_id TEXT NOT NULL,
				ts_ms INTEGER NOT NULL DEFAULT 0,
				item_index INTEGER NOT NULL DEFAULT 0,
				kind TEXT NOT NULL,
				pane_ref TEXT NOT NULL DEFAULT '',
				title TEXT NOT NULL DEFAULT '',
				level TEXT NOT NULL DEFAULT '',
				message TEXT NOT NULL DEFAULT '',
				url TEXT NOT NULL DEFAULT '',
				method TEXT NOT NULL DEFAULT '',
				status_code INTEGER NOT NULL DEFAULT 0,
				duration_ms INTEGER NOT NULL DEFAULT 0,
				selector TEXT NOT NULL DEFAULT '',
				text_value TEXT NOT NULL DEFAULT '',
				trace_id TEXT NOT NULL DEFAULT '',
				linked_event_id TEXT NOT NULL DEFAULT '',
				linked_issue_id TEXT NOT NULL DEFAULT '',
				payload_ref TEXT NOT NULL DEFAULT '',
				meta_json TEXT NOT NULL DEFAULT '{}'
			);
			CREATE INDEX IF NOT EXISTS idx_replay_timeline_manifest_window
				ON replay_timeline_items(manifest_id, ts_ms, item_index);
			CREATE INDEX IF NOT EXISTS idx_replay_timeline_manifest_pane
				ON replay_timeline_items(manifest_id, pane_ref, ts_ms, item_index);
			CREATE INDEX IF NOT EXISTS idx_replay_timeline_manifest_event
				ON replay_timeline_items(manifest_id, linked_event_id, ts_ms, item_index);
			CREATE INDEX IF NOT EXISTS idx_replay_timeline_manifest_issue
				ON replay_timeline_items(manifest_id, linked_issue_id, ts_ms, item_index);
		`},
	{35, `
			CREATE TABLE IF NOT EXISTS project_replay_configs (
				project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
				sample_rate REAL NOT NULL DEFAULT 1.0,
				max_bytes INTEGER NOT NULL DEFAULT 10485760,
				scrub_fields_json TEXT NOT NULL DEFAULT '[]',
				scrub_selectors_json TEXT NOT NULL DEFAULT '[]',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
		`},
	{46, `
			ALTER TABLE artifacts ADD COLUMN organization_id TEXT;
			CREATE INDEX IF NOT EXISTS idx_artifacts_org_release ON artifacts(organization_id, release_version);
	`},
	{78, `
			CREATE TABLE IF NOT EXISTS preprod_artifacts (
				id TEXT PRIMARY KEY,
				organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				build_id TEXT NOT NULL,
				state TEXT NOT NULL DEFAULT 'PROCESSED',
				app_id TEXT,
				app_name TEXT,
				app_version TEXT,
				build_number INTEGER,
				artifact_type TEXT,
				app_date_added TEXT,
				app_date_built TEXT,
				git_info_json TEXT NOT NULL DEFAULT 'null',
				platform TEXT,
				build_configuration TEXT,
				is_installable INTEGER NOT NULL DEFAULT 0,
				install_url TEXT,
				download_count INTEGER NOT NULL DEFAULT 0,
				release_notes TEXT,
				install_groups_json TEXT NOT NULL DEFAULT 'null',
				is_code_signature_valid INTEGER,
				profile_name TEXT,
				codesigning_type TEXT,
				main_binary_identifier TEXT,
				default_base_artifact_id TEXT,
				analysis_state TEXT NOT NULL DEFAULT 'NOT_RAN',
				analysis_error_code TEXT,
				analysis_error_message TEXT,
				download_size INTEGER,
				install_size INTEGER,
				analysis_duration REAL,
				analysis_version TEXT,
				insights_json TEXT NOT NULL DEFAULT 'null',
				app_components_json TEXT NOT NULL DEFAULT 'null',
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_preprod_artifacts_project_created
				ON preprod_artifacts(project_id, created_at DESC, id DESC);
			CREATE INDEX IF NOT EXISTS idx_preprod_artifacts_org
				ON preprod_artifacts(organization_id, id);
			CREATE INDEX IF NOT EXISTS idx_preprod_artifacts_lookup
				ON preprod_artifacts(project_id, app_id, platform, is_installable, created_at DESC);
	`},
	{79, `
			CREATE TABLE IF NOT EXISTS issue_autofix_runs (
				run_id INTEGER PRIMARY KEY AUTOINCREMENT,
				organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
				project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
				issue_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
				status TEXT NOT NULL DEFAULT 'COMPLETED',
				event_id TEXT,
				stopping_point TEXT NOT NULL DEFAULT 'root_cause',
				payload_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_issue_autofix_runs_issue_latest
				ON issue_autofix_runs(issue_id, run_id DESC);
			CREATE INDEX IF NOT EXISTS idx_issue_autofix_runs_project_latest
				ON issue_autofix_runs(project_id, run_id DESC);
	`},
}
