CREATE TABLE `project` (
          `id` text PRIMARY KEY,
          `worktree` text NOT NULL,
          `vcs` text,
          `name` text,
          `icon_url` text,
          `icon_url_override` text,
          `icon_color` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `time_initialized` integer,
          `sandboxes` text NOT NULL,
          `commands` text
        );
CREATE TABLE `session` (
          `id` text PRIMARY KEY,
          `project_id` text NOT NULL,
          `workspace_id` text,
          `parent_id` text,
          `slug` text NOT NULL,
          `directory` text NOT NULL,
          `path` text,
          `title` text NOT NULL,
          `version` text NOT NULL,
          `share_url` text,
          `summary_additions` integer,
          `summary_deletions` integer,
          `summary_files` integer,
          `summary_diffs` text,
          `metadata` text,
          `cost` real DEFAULT 0 NOT NULL,
          `tokens_input` integer DEFAULT 0 NOT NULL,
          `tokens_output` integer DEFAULT 0 NOT NULL,
          `tokens_reasoning` integer DEFAULT 0 NOT NULL,
          `tokens_cache_read` integer DEFAULT 0 NOT NULL,
          `tokens_cache_write` integer DEFAULT 0 NOT NULL,
          `revert` text,
          `permission` text,
          `agent` text,
          `model` text,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `time_compacting` integer,
          `time_archived` integer,
          CONSTRAINT `fk_session_project_id_project_id_fk` FOREIGN KEY (`project_id`) REFERENCES `project`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `message` (
          `id` text PRIMARY KEY,
          `session_id` text NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `fk_message_session_id_session_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `part` (
          `id` text PRIMARY KEY,
          `message_id` text NOT NULL,
          `session_id` text NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `fk_part_message_id_message_id_fk` FOREIGN KEY (`message_id`) REFERENCES `message`(`id`) ON DELETE CASCADE
        );
CREATE TABLE `session_message` (
          `id` text PRIMARY KEY,
          `session_id` text NOT NULL,
          `type` text NOT NULL,
          `seq` integer NOT NULL,
          `time_created` integer NOT NULL,
          `time_updated` integer NOT NULL,
          `data` text NOT NULL,
          CONSTRAINT `fk_session_message_session_id_session_id_fk` FOREIGN KEY (`session_id`) REFERENCES `session`(`id`) ON DELETE CASCADE
        );
CREATE INDEX `message_session_time_created_id_idx` ON `message` (`session_id`,`time_created`,`id`);
CREATE INDEX `part_message_id_id_idx` ON `part` (`message_id`,`id`);
CREATE INDEX `part_session_idx` ON `part` (`session_id`);
CREATE UNIQUE INDEX `session_message_session_seq_idx` ON `session_message` (`session_id`,`seq`);
CREATE INDEX `session_message_session_time_created_id_idx` ON `session_message` (`session_id`,`time_created`,`id`);
CREATE INDEX `session_message_session_type_seq_idx` ON `session_message` (`session_id`,`type`,`seq`);
CREATE INDEX `session_message_time_created_idx` ON `session_message` (`time_created`);
CREATE INDEX `session_parent_idx` ON `session` (`parent_id`);
CREATE INDEX `session_project_idx` ON `session` (`project_id`);
CREATE INDEX `session_workspace_idx` ON `session` (`workspace_id`);
INSERT INTO project VALUES('86846d63407eff503a06c0f194449d3d6efe34bb','/home/dev/demo','git',NULL,NULL,NULL,NULL,1794843567717,1794843676727,NULL,'[]',NULL);
INSERT INTO session VALUES('ses_e1afc9889ffebt8bqDqO6kddcc','86846d63407eff503a06c0f194449d3d6efe34bb',NULL,NULL,'proud-tiger','/home/dev/demo','','Canned answer number 2. Hello from the fake model.','1.19.5',NULL,0,0,0,NULL,NULL,0.0,10,5,0,0,0,NULL,'[{"permission":"question","pattern":"*","action":"deny"},{"permission":"plan_enter","pattern":"*","action":"deny"},{"permission":"plan_exit","pattern":"*","action":"deny"}]','build','{"id":"fake-model","providerID":"fake","variant":"default"}',1794843567990,1794843568933,NULL,NULL);
INSERT INTO session VALUES('ses_e1afc8187ffemRsb9igmZ1B574','86846d63407eff503a06c0f194449d3d6efe34bb',NULL,NULL,'curious-eagle','/home/dev/demo','','Canned answer number 2. Hello from the fake model.','1.19.5',NULL,0,0,0,NULL,NULL,0.0,30,15,0,0,0,NULL,'[{"permission":"question","pattern":"*","action":"deny"},{"permission":"plan_enter","pattern":"*","action":"deny"},{"permission":"plan_exit","pattern":"*","action":"deny"}]','build','{"id":"fake-model","providerID":"fake","variant":"default"}',1794843573880,1794843576463,NULL,NULL);
INSERT INTO session VALUES('ses_e1afc7488ffeLuMKbKanDq17wr','86846d63407eff503a06c0f194449d3d6efe34bb',NULL,NULL,'curious-garden','/home/dev/demo/sub/dir','sub/dir','Canned answer number 2. Hello from the fake model.','1.19.5',NULL,0,0,0,NULL,NULL,0.0,10,5,0,0,0,NULL,'[{"permission":"question","pattern":"*","action":"deny"},{"permission":"plan_enter","pattern":"*","action":"deny"},{"permission":"plan_exit","pattern":"*","action":"deny"}]','build','{"id":"fake-model","providerID":"fake","variant":"default"}',1794843577208,1794843578079,NULL,NULL);
INSERT INTO session VALUES('ses_e1afaeeffffe4QQBc5xniIBpGu','86846d63407eff503a06c0f194449d3d6efe34bb',NULL,NULL,'neon-otter','/home/dev/demo','','Canned answer number 2. Hello from the fake model.','1.19.5',NULL,0,0,0,NULL,NULL,0.0,10,5,0,0,0,NULL,'[{"permission":"question","pattern":"*","action":"deny"},{"permission":"plan_enter","pattern":"*","action":"deny"},{"permission":"plan_exit","pattern":"*","action":"deny"}]','build','{"id":"fake-model","providerID":"fake","variant":"default"}',1794843676929,1794843677946,NULL,NULL);
INSERT INTO message VALUES('msg_1e5036790001nBdGBDksmsWTwF','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568016,1794843568022,'{"role":"user","time":{"created":1794843568016},"agent":"build","model":{"providerID":"fake","modelID":"fake-model"}}');
INSERT INTO message VALUES('msg_1e503699c001iaZ1stk3Ar5ZDx','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568540,1794843568924,'{"parentID":"msg_1e5036790001nBdGBDksmsWTwF","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843568540,"completed":1794843568923},"finish":"stop"}');
INSERT INTO message VALUES('msg_1e5037e91001dn602QVm9wfv2b','ses_e1afc8187ffemRsb9igmZ1B574',1794843573905,1794843573914,'{"role":"user","time":{"created":1794843573905},"agent":"build","model":{"providerID":"fake","modelID":"fake-model"}}');
INSERT INTO message VALUES('msg_1e5038080001T2hF7h4JJlzuce','ses_e1afc8187ffemRsb9igmZ1B574',1794843574400,1794843574798,'{"parentID":"msg_1e5037e91001dn602QVm9wfv2b","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843574400,"completed":1794843574797},"finish":"tool-calls"}');
INSERT INTO message VALUES('msg_1e50382100015fIOvuw5GpPp7o','ses_e1afc8187ffemRsb9igmZ1B574',1794843574800,1794843574827,'{"parentID":"msg_1e5037e91001dn602QVm9wfv2b","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843574800,"completed":1794843574826},"finish":"stop"}');
INSERT INTO message VALUES('msg_1e5038538001hvz6iAU57EJOvN','ses_e1afc8187ffemRsb9igmZ1B574',1794843575608,1794843575613,'{"role":"user","time":{"created":1794843575608},"agent":"build","model":{"providerID":"fake","modelID":"fake-model"}}');
INSERT INTO message VALUES('msg_1e5038733001FFkocpkFy9njyu','ses_e1afc8187ffemRsb9igmZ1B574',1794843576115,1794843576452,'{"parentID":"msg_1e5038538001hvz6iAU57EJOvN","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843576115,"completed":1794843576452},"finish":"stop"}');
INSERT INTO message VALUES('msg_1e5038b920011U5581thjbtPrA','ses_e1afc7488ffeLuMKbKanDq17wr',1794843577234,1794843577241,'{"role":"user","time":{"created":1794843577234},"agent":"build","model":{"providerID":"fake","modelID":"fake-model"}}');
INSERT INTO message VALUES('msg_1e5038d7c001gqT6usEkO85hD8','ses_e1afc7488ffeLuMKbKanDq17wr',1794843577724,1794843578069,'{"parentID":"msg_1e5038b920011U5581thjbtPrA","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo/sub/dir","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843577724,"completed":1794843578068},"finish":"stop"}');
INSERT INTO message VALUES('msg_1e5051177001UR5hhlOw2z6PDS','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677047,1794843677055,'{"role":"user","time":{"created":1794843677047},"agent":"build","model":{"providerID":"fake","modelID":"fake-model"}}');
INSERT INTO message VALUES('msg_1e5051374001zl4k6t87AaPgGb','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677556,1794843677935,'{"parentID":"msg_1e5051177001UR5hhlOw2z6PDS","role":"assistant","mode":"build","agent":"build","path":{"cwd":"/home/dev/demo","root":"/home/dev/demo"},"cost":0,"tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"modelID":"fake-model","providerID":"fake","time":{"created":1794843677556,"completed":1794843677935},"finish":"stop"}');
INSERT INTO part VALUES('prt_1e5036794001DqMBYXAioEMY1P','msg_1e5036790001nBdGBDksmsWTwF','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568023,1794843568024,'{"type":"text","text":"\"Say hello single turn\""}');
INSERT INTO part VALUES('prt_1e5036b10001GweceqkCujbXpR','msg_1e503699c001iaZ1stk3Ar5ZDx','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568912,1794843568913,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e5036b12001Ol3yceiW0oiRBT','msg_1e503699c001iaZ1stk3Ar5ZDx','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568914,1794843568918,'{"type":"text","text":"Canned answer number 1. Hello from the fake model.","time":{"start":1794843568914,"end":1794843568917}}');
INSERT INTO part VALUES('prt_1e5036b17001E4U65VFJqlMCap','msg_1e503699c001iaZ1stk3Ar5ZDx','ses_e1afc9889ffebt8bqDqO6kddcc',1794843568919,1794843568920,'{"reason":"stop","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
INSERT INTO part VALUES('prt_1e5037e97001rFjI2mrnff2KLv','msg_1e5037e91001dn602QVm9wfv2b','ses_e1afc8187ffemRsb9igmZ1B574',1794843573915,1794843573916,'{"type":"text","text":"\"Please RUNTOOL and tell me the output\""}');
INSERT INTO part VALUES('prt_1e50381c8001IHOKjnpm2v3Qsh','msg_1e5038080001T2hF7h4JJlzuce','ses_e1afc8187ffemRsb9igmZ1B574',1794843574728,1794843574730,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e50381cb001wgxPy8En9gDk9e','msg_1e5038080001T2hF7h4JJlzuce','ses_e1afc8187ffemRsb9igmZ1B574',1794843574731,1794843574760,'{"type":"text","text":"I will run a command. ","time":{"start":1794843574731,"end":1794843574759}}');
INSERT INTO part VALUES('prt_1e50381ce001OU1MhFuYA9d7sp','msg_1e5038080001T2hF7h4JJlzuce','ses_e1afc8187ffemRsb9igmZ1B574',1794843574734,1794843574791,'{"type":"tool","tool":"bash","callID":"call_b1ed0ad0","state":{"status":"completed","input":{"command":"echo cortex-check"},"output":"cortex-check\n","metadata":{"output":"cortex-check\n","exit":0,"truncated":false},"title":"echo cortex-check","time":{"start":1794843574739,"end":1794843574790}}}');
INSERT INTO part VALUES('prt_1e5038209001l48UXaWW0d7P2E','msg_1e5038080001T2hF7h4JJlzuce','ses_e1afc8187ffemRsb9igmZ1B574',1794843574793,1794843574794,'{"reason":"tool-calls","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
INSERT INTO part VALUES('prt_1e50382200018rm4qd6sEkUMXs','msg_1e50382100015fIOvuw5GpPp7o','ses_e1afc8187ffemRsb9igmZ1B574',1794843574816,1794843574817,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e50382220010vI4nGbflEMExU','msg_1e50382100015fIOvuw5GpPp7o','ses_e1afc8187ffemRsb9igmZ1B574',1794843574818,1794843574823,'{"type":"text","text":"Tool done: output was cortex-check. FINAL-ANSWER","time":{"start":1794843574818,"end":1794843574822}}');
INSERT INTO part VALUES('prt_1e5038227001sFYEvyJ43YGGst','msg_1e50382100015fIOvuw5GpPp7o','ses_e1afc8187ffemRsb9igmZ1B574',1794843574824,1794843574824,'{"reason":"stop","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
INSERT INTO part VALUES('prt_1e503853a001297bMQ01xDadYG','msg_1e5038538001hvz6iAU57EJOvN','ses_e1afc8187ffemRsb9igmZ1B574',1794843575615,1794843575616,'{"type":"text","text":"\"Second prompt of the same session\""}');
INSERT INTO part VALUES('prt_1e5038875001WJLRLY1LEHN2Cx','msg_1e5038733001FFkocpkFy9njyu','ses_e1afc8187ffemRsb9igmZ1B574',1794843576437,1794843576438,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e5038877001IUtlzwRRAHsQq3','msg_1e5038733001FFkocpkFy9njyu','ses_e1afc8187ffemRsb9igmZ1B574',1794843576439,1794843576445,'{"type":"text","text":"Canned answer number 2. Hello from the fake model.","time":{"start":1794843576439,"end":1794843576444}}');
INSERT INTO part VALUES('prt_1e5038880001tug5Tb7ad7oodI','msg_1e5038733001FFkocpkFy9njyu','ses_e1afc8187ffemRsb9igmZ1B574',1794843576448,1794843576449,'{"reason":"stop","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
INSERT INTO part VALUES('prt_1e5038b97001Q4FZmY6567SrrH','msg_1e5038b920011U5581thjbtPrA','ses_e1afc7488ffeLuMKbKanDq17wr',1794843577241,1794843577242,'{"type":"text","text":"\"Hello from the subdirectory\""}');
INSERT INTO part VALUES('prt_1e5038ec4001rE5Tk2nlmhwoUT','msg_1e5038d7c001gqT6usEkO85hD8','ses_e1afc7488ffeLuMKbKanDq17wr',1794843578052,1794843578054,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e5038ec7001r2gWleaqrQnzcS','msg_1e5038d7c001gqT6usEkO85hD8','ses_e1afc7488ffeLuMKbKanDq17wr',1794843578055,1794843578060,'{"type":"text","text":"Canned answer number 1. Hello from the fake model.","time":{"start":1794843578055,"end":1794843578059}}');
INSERT INTO part VALUES('prt_1e5038ecf001CY92VY3q5F0yWP','msg_1e5038d7c001gqT6usEkO85hD8','ses_e1afc7488ffeLuMKbKanDq17wr',1794843578063,1794843578064,'{"reason":"stop","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
INSERT INTO part VALUES('prt_1e505117d001v2cavKwnJSg8iq','msg_1e5051177001UR5hhlOw2z6PDS','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677056,1794843677057,'{"type":"text","text":"Unquoted words here"}');
INSERT INTO part VALUES('prt_1e50514e2001RyLKjkc9yLZOxi','msg_1e5051374001zl4k6t87AaPgGb','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677922,1794843677923,'{"type":"step-start"}');
INSERT INTO part VALUES('prt_1e50514e5001JceU3yUvzPbYJ0','msg_1e5051374001zl4k6t87AaPgGb','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677925,1794843677929,'{"type":"text","text":"Canned answer number 1. Hello from the fake model.","time":{"start":1794843677925,"end":1794843677928}}');
INSERT INTO part VALUES('prt_1e50514ea001u7tjYeMCdEYYT2','msg_1e5051374001zl4k6t87AaPgGb','ses_e1afaeeffffe4QQBc5xniIBpGu',1794843677930,1794843677931,'{"reason":"stop","type":"step-finish","tokens":{"total":15,"input":10,"output":5,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}');
