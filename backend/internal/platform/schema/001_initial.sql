-- V1 首次部署。已有库升级必须单独增加迁移，禁止修改已运行的脚本。

CREATE TABLE IF NOT EXISTS sys_user (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  display_name VARCHAR(255) NOT NULL COMMENT '显示名称',
  avatar_url VARCHAR(1024) NOT NULL COMMENT '头像地址，空为未提供',
  role VARCHAR(50) NOT NULL COMMENT 'USER 普通用户、ADMIN 管理员',
  account_status VARCHAR(50) NOT NULL COMMENT 'ACTIVE 可用、DISABLED 停用',
  last_login_time BIGINT NOT NULL COMMENT '最后登录毫秒，0 为尚未登录',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='平台统一用户';


CREATE TABLE IF NOT EXISTS sys_dingtalk_identity (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  sys_user_id BIGINT NOT NULL COMMENT '平台用户 ID',
  corp_id VARCHAR(128) NOT NULL COMMENT '企业 ID',
  app_id VARCHAR(128) NOT NULL COMMENT '应用 ID',
  identity_type VARCHAR(50) NOT NULL COMMENT 'USER_ID、UNION_ID、OPEN_ID',
  identity_value VARCHAR(255) NOT NULL COMMENT '非空身份值',
  verified_time BIGINT NOT NULL COMMENT '身份验证毫秒',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_identity (corp_id,app_id,identity_type,identity_value),
  KEY idx_user (sys_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='经过验证的钉钉身份';


CREATE TABLE IF NOT EXISTS sys_audit_log (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  actor_sys_user_id BIGINT NOT NULL COMMENT '操作者 ID，0 为系统',
  actor_type VARCHAR(50) NOT NULL COMMENT 'USER、SYSTEM',
  action_type VARCHAR(50) NOT NULL COMMENT '操作类型',
  target_type VARCHAR(50) NOT NULL COMMENT '目标实体',
  target_id BIGINT NOT NULL COMMENT '目标 ID，0 为尚未创建',
  request_key VARCHAR(128) NOT NULL COMMENT '请求唯一键',
  before_data JSON NOT NULL COMMENT '变更前脱敏数据',
  after_data JSON NOT NULL COMMENT '变更后脱敏数据',
  result_status VARCHAR(50) NOT NULL COMMENT 'SUCCESS、FAILED',
  occurred_time BIGINT NOT NULL COMMENT '发生毫秒',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_request (request_key),
  KEY idx_target (target_type,target_id,occurred_time,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='管理操作审计';


CREATE TABLE IF NOT EXISTS alert_project (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  project_code VARCHAR(100) NOT NULL COMMENT '唯一项目编码',
  name VARCHAR(255) NOT NULL COMMENT '项目名称',
  workspace_root VARCHAR(700) NOT NULL COMMENT '规范化工作区父目录',
  runner_image VARCHAR(255) NOT NULL COMMENT '固定版本运行镜像',
  credential_ref VARCHAR(255) NOT NULL COMMENT '运行凭据环境引用，空为未配置',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (project_code),
  UNIQUE KEY uk_root (workspace_root)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='业务项目';


CREATE TABLE IF NOT EXISTS alert_project_workspace (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_project_id BIGINT NOT NULL COMMENT '项目 ID',
  slot_no INT NOT NULL COMMENT '槽位 1、2、3',
  workspace_path VARCHAR(700) NOT NULL COMMENT '规范化实际路径',
  workspace_status VARCHAR(50) NOT NULL COMMENT 'READY、OCCUPIED、BLOCKED',
  active_alert_ai_task_id BIGINT NOT NULL COMMENT '占用任务 ID，0 为无占用',
  assignment_version BIGINT NOT NULL COMMENT '单调递增分配版本',
  blocked_reason VARCHAR(255) NOT NULL COMMENT '阻塞说明，空为无',
  last_check_time BIGINT NOT NULL COMMENT '最后检查毫秒，0 为未检查',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_slot (alert_project_id,slot_no),
  UNIQUE KEY uk_path (workspace_path),
  KEY idx_ready (alert_project_id,workspace_status,slot_no),
  CHECK (slot_no BETWEEN 1 AND 3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='固定修复工作区';


CREATE TABLE IF NOT EXISTS alert_project_repo (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_project_id BIGINT NOT NULL COMMENT '项目 ID',
  repo_code VARCHAR(100) NOT NULL COMMENT '项目内仓库编码',
  relative_path VARCHAR(512) NOT NULL COMMENT '工作区下相对路径',
  provider VARCHAR(50) NOT NULL COMMENT 'CODEUP',
  organization_id VARCHAR(128) NOT NULL COMMENT 'Codeup 组织 ID',
  repository_id VARCHAR(128) NOT NULL COMMENT 'Codeup 仓库 ID',
  git_url VARCHAR(1024) NOT NULL COMMENT 'Git 地址，不含凭据',
  base_branch VARCHAR(50) NOT NULL COMMENT '固定 master',
  credential_ref VARCHAR(255) NOT NULL COMMENT '凭据环境引用，空为未配置',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (alert_project_id,repo_code),
  UNIQUE KEY uk_path (alert_project_id,relative_path),
  KEY idx_provider (organization_id,repository_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='项目固定仓库';


CREATE TABLE IF NOT EXISTS alert_source (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  source_code VARCHAR(100) NOT NULL COMMENT '来源唯一编码',
  source_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC',
  environment VARCHAR(100) NOT NULL COMMENT '来源环境',
  adapter_type VARCHAR(50) NOT NULL COMMENT 'SLS、ALERTMANAGER',
  auth_ref VARCHAR(255) NOT NULL COMMENT '鉴权密钥环境引用',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (source_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='可信 Hook 来源';


CREATE TABLE IF NOT EXISTS alert_resource (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_source_id BIGINT NOT NULL COMMENT '来源 ID',
  alert_project_id BIGINT NOT NULL COMMENT '项目 ID',
  resource_type VARCHAR(50) NOT NULL COMMENT 'SERVICE、INSTANCE',
  resource_key VARCHAR(400) NOT NULL COMMENT '服务键或实例 ID',
  name VARCHAR(255) NOT NULL COMMENT '显示名称',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_resource (alert_source_id,resource_type,resource_key),
  KEY idx_project (alert_project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='来源内资源';


CREATE TABLE IF NOT EXISTS alert_group (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  corp_id VARCHAR(128) NOT NULL COMMENT '企业 ID',
  app_id VARCHAR(128) NOT NULL COMMENT '应用 ID',
  conversation_id VARCHAR(256) NOT NULL COMMENT '钉钉 openConversationId',
  name VARCHAR(255) NOT NULL COMMENT '群名称',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  integration_status VARCHAR(50) NOT NULL COMMENT 'UNVERIFIED、READY、FAILED',
  last_error VARCHAR(255) NOT NULL COMMENT '同步错误，空为无',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_group (corp_id,app_id,conversation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='固定告警群';


CREATE TABLE IF NOT EXISTS alert_route (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_resource_id BIGINT NOT NULL COMMENT '资源 ID',
  alert_group_id BIGINT NOT NULL COMMENT '群 ID',
  alert_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC、NOTICE',
  config_status VARCHAR(50) NOT NULL COMMENT 'ENABLED、DISABLED',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_route (alert_resource_id,alert_type),
  KEY idx_group (alert_group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='资源类型到群固定路由';


CREATE TABLE IF NOT EXISTS alert_group_member (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_group_id BIGINT NOT NULL COMMENT '群 ID',
  sys_user_id BIGINT NOT NULL COMMENT '平台用户 ID',
  member_status VARCHAR(50) NOT NULL COMMENT 'MEMBER、LEFT、UNKNOWN',
  verified_time BIGINT NOT NULL COMMENT '确认毫秒',
  expire_time BIGINT NOT NULL COMMENT '过期毫秒，不作为永久授权',
  verification_source VARCHAR(50) NOT NULL COMMENT 'DINGTALK、DEMO',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_member (alert_group_id,sys_user_id),
  KEY idx_user (sys_user_id,member_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='已验证成员快照';


CREATE TABLE IF NOT EXISTS alert_problem (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_resource_id BIGINT NOT NULL COMMENT '资源 ID',
  alert_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC',
  key_version INT NOT NULL COMMENT '分组键版本 1',
  grouping_key VARCHAR(64) NOT NULL COMMENT '组成字段规范编码 SHA256',
  grouping_data JSON NOT NULL COMMENT '原始组成字段',
  title VARCHAR(500) NOT NULL COMMENT '问题摘要',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_problem (alert_resource_id,alert_type,key_version,grouping_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='稳定告警问题';


CREATE TABLE IF NOT EXISTS alert_round (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_problem_id BIGINT NOT NULL COMMENT '问题 ID',
  alert_group_id BIGINT NOT NULL COMMENT '固定归属群 ID',
  alert_project_id BIGINT NOT NULL COMMENT '创建时项目 ID',
  alert_resource_id BIGINT NOT NULL COMMENT '创建时资源 ID',
  alert_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC',
  alert_no VARCHAR(32) NOT NULL COMMENT 'AL 加轮次 ID，不变编号',
  round_no BIGINT NOT NULL COMMENT '问题内递增轮次',
  round_status VARCHAR(50) NOT NULL COMMENT 'PENDING、PROCESSING、HANDLED、RECOVERED',
  open_marker BIGINT NOT NULL COMMENT '未结束为 0，终态为本行 ID',
  cycle_key VARCHAR(100) NOT NULL COMMENT '日志轮次键或精确指标周期',
  cycle_data JSON NOT NULL COMMENT '周期原始依据',
  event_count BIGINT NOT NULL COMMENT '已归属事件次数',
  count_quality VARCHAR(50) NOT NULL COMMENT 'EXACT、BEST_EFFORT',
  first_event_time BIGINT NOT NULL COMMENT '最早发生毫秒',
  last_event_time BIGINT NOT NULL COMMENT '最近发生毫秒',
  last_receive_time BIGINT NOT NULL COMMENT '最近接收毫秒',
  end_time BIGINT NOT NULL COMMENT '结束毫秒，0 为未结束',
  end_record_time BIGINT NOT NULL COMMENT '结束落库毫秒，0 为未结束',
  handled_sys_user_id BIGINT NOT NULL COMMENT '处理人 ID，0 为系统或未结束',
  result_text MEDIUMTEXT NOT NULL COMMENT '人工结果，空为未处理',
  version BIGINT NOT NULL COMMENT '单调展示版本',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_no (alert_no),
  UNIQUE KEY uk_round (alert_problem_id,round_no),
  UNIQUE KEY uk_open (alert_problem_id,open_marker),
  UNIQUE KEY uk_cycle (alert_problem_id,cycle_key),
  KEY idx_group (alert_group_id,round_status,alert_type,first_event_time,id),
  KEY idx_group_open (alert_group_id,open_marker,first_event_time,id),
  KEY idx_time (first_event_time,id),
  KEY idx_project (alert_project_id,first_event_time,id),
  KEY idx_end (end_time,alert_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='一次告警轮次';


CREATE TABLE IF NOT EXISTS alert_event (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_source_id BIGINT NOT NULL COMMENT '可信来源 ID',
  alert_resource_id BIGINT NOT NULL COMMENT '资源 ID，0 为未匹配',
  alert_round_id BIGINT NOT NULL COMMENT '轮次 ID，0 为尚未归属或 NOTICE',
  alert_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC、NOTICE',
  event_type VARCHAR(50) NOT NULL COMMENT 'LOG、FIRING、RESOLVED、NOTICE',
  dedup_key VARCHAR(128) NOT NULL COMMENT '非空去重键',
  dedup_quality VARCHAR(50) NOT NULL COMMENT 'STRONG、WEAK、NONE',
  source_event_id VARCHAR(512) NOT NULL COMMENT '可信来源投递身份，空为缺失',
  payload_hash VARCHAR(64) NOT NULL COMMENT '业务内容 SHA256',
  occurred_time BIGINT NOT NULL COMMENT '发生毫秒，0 为上游未提供',
  received_time BIGINT NOT NULL COMMENT '接收毫秒',
  source_time_raw VARCHAR(64) NOT NULL COMMENT '原始时间，空为未知',
  payload JSON NOT NULL COMMENT '规范化证据，展示时脱敏',
  process_status VARCHAR(50) NOT NULL COMMENT 'RECEIVED、APPLIED、NEEDS_REVIEW、FAILED',
  process_error VARCHAR(255) NOT NULL COMMENT '归属或处理失败说明',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_dedup (alert_source_id,dedup_key),
  KEY idx_pending (process_status,received_time,id),
  KEY idx_round (alert_round_id,occurred_time,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='持久化 Hook 事件';


CREATE TABLE IF NOT EXISTS alert_log_detail (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_event_id BIGINT NOT NULL COMMENT '事件 ID',
  namespace_name VARCHAR(255) NOT NULL COMMENT '命名空间',
  container_name VARCHAR(255) NOT NULL COMMENT '容器名',
  service_key VARCHAR(400) NOT NULL COMMENT '命名空间加下划线加容器名',
  trace_id VARCHAR(255) NOT NULL COMMENT '追踪 ID，空为未提供',
  class_name VARCHAR(255) NOT NULL COMMENT '异常类',
  message_sha VARCHAR(64) NOT NULL COMMENT '完整消息 SHA256',
  message MEDIUMTEXT NOT NULL COMMENT '完整消息',
  source_level VARCHAR(50) NOT NULL COMMENT '来源级别',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_event (alert_event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='日志或通知详情';


CREATE TABLE IF NOT EXISTS alert_metric_detail (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_event_id BIGINT NOT NULL COMMENT '事件 ID',
  instance_id VARCHAR(400) NOT NULL COMMENT '实例 ID',
  instance_name VARCHAR(255) NOT NULL COMMENT '实例名称',
  metric_key VARCHAR(255) NOT NULL COMMENT '指标键',
  metric_rule VARCHAR(1024) NOT NULL COMMENT '规则说明',
  metric_value_raw VARCHAR(512) NOT NULL COMMENT '当前事件指标值，空为缺失',
  value_status VARCHAR(50) NOT NULL COMMENT 'AVAILABLE、MISSING、INVALID',
  fingerprint VARCHAR(255) NOT NULL COMMENT '可选上游指纹',
  starts_at_raw VARCHAR(64) NOT NULL COMMENT '原始开始时间，保留纳秒',
  ends_at_raw VARCHAR(64) NOT NULL COMMENT '原始结束时间，空为未恢复',
  starts_at BIGINT NOT NULL COMMENT '开始毫秒',
  ends_at BIGINT NOT NULL COMMENT '结束毫秒，0 为未恢复',
  labels JSON NOT NULL COMMENT '完整标签',
  annotations JSON NOT NULL COMMENT '完整注解',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_event (alert_event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='指标事件详情';


CREATE TABLE IF NOT EXISTS alert_record (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_round_id BIGINT NOT NULL COMMENT '轮次 ID',
  actor_sys_user_id BIGINT NOT NULL COMMENT '操作者 ID，0 为非人工',
  actor_type VARCHAR(50) NOT NULL COMMENT 'USER、AI、SYSTEM',
  actor_name VARCHAR(255) NOT NULL COMMENT '当时名称',
  record_type VARCHAR(50) NOT NULL COMMENT 'NOTE、LOG_HANDLED、METRIC_RECOVERED、AI_PROGRESS、AI_RESULT、MR_PROGRESS',
  entry_point VARCHAR(50) NOT NULL COMMENT 'CARD、H5、HOOK、RUNNER',
  operation_key VARCHAR(128) NOT NULL COMMENT '轮次内幂等键',
  request_hash VARCHAR(64) NOT NULL COMMENT '绑定操作者及内容的摘要',
  content MEDIUMTEXT NOT NULL COMMENT '处理内容',
  structured_data JSON NOT NULL COMMENT '结构化详情',
  occurred_time BIGINT NOT NULL COMMENT '发生毫秒',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_operation (alert_round_id,operation_key),
  KEY idx_round (alert_round_id,occurred_time,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='追加式处理历史';


CREATE TABLE IF NOT EXISTS alert_ai_task (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_round_id BIGINT NOT NULL COMMENT '日志轮次 ID',
  alert_project_id BIGINT NOT NULL COMMENT '项目 ID',
  alert_project_workspace_id BIGINT NOT NULL COMMENT '工作区 ID，0 为未分配',
  execution_status VARCHAR(50) NOT NULL COMMENT 'QUEUED、STARTING、RUNNING、FINALIZING、SUCCEEDED、FAILED、SKIPPED',
  active_marker BIGINT NOT NULL COMMENT '活动为 0，其余为本行 ID',
  workspace_version BIGINT NOT NULL COMMENT '分配版本，0 为未分配',
  phase VARCHAR(50) NOT NULL COMMENT 'WAITING、DIAGNOSING、LOCATED、REPAIRING、VERIFYING、DELIVERING、FINISHED',
  business_result VARCHAR(50) NOT NULL COMMENT 'UNDETERMINED、FIX_PROPOSED、NO_ISSUE、UNRESOLVED',
  delivery_status VARCHAR(50) NOT NULL COMMENT 'PENDING、NOT_REQUIRED、COMPLETE、PARTIAL_FAILED',
  delivery_set_status VARCHAR(50) NOT NULL COMMENT 'OPEN、SEALED',
  branch_name VARCHAR(100) NOT NULL COMMENT '定位后分配分支，空为尚未定位',
  context_snapshot JSON NOT NULL COMMENT '执行时项目和仓库配置快照',
  runner_image VARCHAR(255) NOT NULL COMMENT '分配时镜像',
  workspace_path VARCHAR(700) NOT NULL COMMENT '分配时目录',
  executor_id VARCHAR(255) NOT NULL COMMENT '宿主执行器身份',
  execution_token_hash VARCHAR(64) NOT NULL COMMENT '任务事件令牌 SHA256',
  container_id VARCHAR(128) NOT NULL COMMENT 'Docker ID，空为未创建',
  container_name VARCHAR(128) NOT NULL COMMENT '稳定容器名',
  last_sequence BIGINT NOT NULL COMMENT '已应用连续事件序号',
  heartbeat_time BIGINT NOT NULL COMMENT '最后活动毫秒',
  artifact_path VARCHAR(700) NOT NULL COMMENT '任务产物目录',
  error_code VARCHAR(50) NOT NULL COMMENT '错误码，空为无',
  error_message MEDIUMTEXT NOT NULL COMMENT '错误说明，空为无',
  queued_time BIGINT NOT NULL COMMENT '排队毫秒',
  claimed_time BIGINT NOT NULL COMMENT '领取毫秒，0 为未领取',
  started_time BIGINT NOT NULL COMMENT '实际开始毫秒，0 为未开始',
  finished_time BIGINT NOT NULL COMMENT '结束毫秒，0 为未结束',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_round (alert_round_id),
  UNIQUE KEY uk_workspace (alert_project_workspace_id,active_marker),
  KEY idx_queue (alert_project_id,execution_status,queued_time,id),
  KEY idx_heartbeat (execution_status,heartbeat_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='每轮一次 AI 任务';


CREATE TABLE IF NOT EXISTS alert_ai_task_repo (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_ai_task_id BIGINT NOT NULL COMMENT '任务 ID',
  alert_project_repo_id BIGINT NOT NULL COMMENT '配置仓库 ID',
  scope_status VARCHAR(50) NOT NULL COMMENT 'REQUIRED、EXCLUDED',
  base_revision VARCHAR(64) NOT NULL COMMENT 'origin/master 基线提交，空为未开始',
  head_revision VARCHAR(64) NOT NULL COMMENT '修复提交，空为未完成',
  branch_name VARCHAR(100) NOT NULL COMMENT '普通修复分支',
  repair_status VARCHAR(50) NOT NULL COMMENT 'PENDING、RUNNING、DONE、FAILED、NOT_NEEDED',
  verification_status VARCHAR(50) NOT NULL COMMENT 'NOT_RUN、PASSED、FAILED、UNAVAILABLE',
  delivery_status VARCHAR(50) NOT NULL COMMENT 'PENDING、DELIVERED、FAILED、NOT_REQUIRED',
  required_mr_count INT NOT NULL COMMENT '封口时必要 MR 数量',
  verification_summary MEDIUMTEXT NOT NULL COMMENT '验证结果',
  error_message MEDIUMTEXT NOT NULL COMMENT '失败说明',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_repo (alert_ai_task_id,alert_project_repo_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='必要仓库交付集合';


CREATE TABLE IF NOT EXISTS alert_merge_request (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_ai_task_repo_id BIGINT NOT NULL COMMENT '任务仓库 ID',
  provider VARCHAR(50) NOT NULL COMMENT 'CODEUP',
  organization_id VARCHAR(128) NOT NULL COMMENT '组织 ID',
  repository_id VARCHAR(128) NOT NULL COMMENT '仓库 ID',
  merge_request_id VARCHAR(128) NOT NULL COMMENT '外部不可变 ID',
  display_number VARCHAR(64) NOT NULL COMMENT '页面显示编号',
  url VARCHAR(1024) NOT NULL COMMENT '验证后的页面地址',
  title VARCHAR(500) NOT NULL COMMENT '标题',
  source_branch VARCHAR(100) NOT NULL COMMENT '修复分支',
  target_branch VARCHAR(50) NOT NULL COMMENT 'master',
  head_revision VARCHAR(64) NOT NULL COMMENT '当前源提交',
  merge_revision VARCHAR(64) NOT NULL COMMENT '合并提交，空为未合并',
  merge_status VARCHAR(50) NOT NULL COMMENT 'OPEN、MERGED、CLOSED',
  review_status VARCHAR(50) NOT NULL COMMENT 'UNKNOWN、PENDING、APPROVED、REJECTED',
  provider_status VARCHAR(50) NOT NULL COMMENT '来源状态',
  provider_update_time BIGINT NOT NULL COMMENT '上游更新毫秒',
  last_verified_time BIGINT NOT NULL COMMENT '查询核实毫秒',
  merged_time BIGINT NOT NULL COMMENT '合并毫秒，0 为未合并',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_external (provider,organization_id,repository_id,merge_request_id),
  KEY idx_repo (alert_ai_task_repo_id,merge_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='独立合并请求';


CREATE TABLE IF NOT EXISTS alert_card (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_round_id BIGINT NOT NULL COMMENT '轮次 ID，NOTICE 为 0',
  alert_event_id BIGINT NOT NULL COMMENT 'NOTICE 事件 ID，普通卡为 0',
  alert_group_id BIGINT NOT NULL COMMENT '群 ID',
  card_type VARCHAR(50) NOT NULL COMMENT 'LOG、METRIC、NOTICE',
  delivery_key VARCHAR(128) NOT NULL COMMENT '业务投递唯一键',
  out_track_id VARCHAR(128) NOT NULL COMMENT '投递前分配的外部唯一键',
  template_id VARCHAR(255) NOT NULL COMMENT '配置的已发布模板 ID',
  template_version VARCHAR(50) NOT NULL COMMENT '模板契约版本',
  delivery_status VARCHAR(50) NOT NULL COMMENT 'PENDING、SENDING、SENT、UNKNOWN、FAILED',
  desired_version BIGINT NOT NULL COMMENT '期望轮次版本',
  sent_version BIGINT NOT NULL COMMENT '确认外部版本，0 为未投递',
  last_error VARCHAR(255) NOT NULL COMMENT '最后错误',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_delivery (delivery_key),
  UNIQUE KEY uk_track (out_track_id),
  KEY idx_round (alert_round_id,id),
  KEY idx_event (alert_event_id,id),
  CHECK ((alert_round_id>0 AND alert_event_id=0 AND card_type IN ('LOG','METRIC')) OR (alert_round_id=0 AND alert_event_id>0 AND card_type='NOTICE'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='消息实例投递状态';


CREATE TABLE IF NOT EXISTS alert_topbox (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  alert_group_id BIGINT NOT NULL COMMENT '群 ID',
  out_track_id VARCHAR(128) NOT NULL COMMENT '外部实例标识，空为未打开',
  desired_status VARCHAR(50) NOT NULL COMMENT 'OPEN、CLOSED',
  observed_status VARCHAR(50) NOT NULL COMMENT 'UNKNOWN、OPEN、CLOSED',
  sync_state VARCHAR(50) NOT NULL DEFAULT 'IDLE' COMMENT 'IDLE、SENDING、UNKNOWN、FAILED、SYNCED',
  desired_version BIGINT NOT NULL COMMENT '期望同步版本',
  sent_version BIGINT NOT NULL COMMENT '已确认同步版本',
  last_error VARCHAR(255) NOT NULL COMMENT '最后同步错误',
  last_sync_time BIGINT NOT NULL COMMENT '最近同步毫秒，0 为未同步',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_group (alert_group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='每群吊顶同步状态';


CREATE TABLE IF NOT EXISTS alert_job (
  id BIGINT NOT NULL COMMENT '应用分配主键',
  job_type VARCHAR(50) NOT NULL COMMENT 'CARD_SEND、ROUND_CARD_SYNC、TOPBOX_SYNC、AI_PROGRESS_APPLY、MR_RECONCILE',
  job_key VARCHAR(191) NOT NULL COMMENT '持久幂等键',
  target_type VARCHAR(50) NOT NULL COMMENT 'CARD、ROUND、GROUP、TASK、MR',
  target_id BIGINT NOT NULL COMMENT '目标 ID，0 为未关联外部 MR',
  target_version BIGINT NOT NULL COMMENT '目标版本或事件序号',
  payload JSON NOT NULL COMMENT '限定类型的任务报文',
  job_status VARCHAR(50) NOT NULL COMMENT 'PENDING、RUNNING、SUCCEEDED、FAILED、UNKNOWN',
  effect_state VARCHAR(50) NOT NULL DEFAULT 'UNSENT' COMMENT 'UNSENT、REQUESTED；外部创建调用先记账后发出',
  attempt_count INT NOT NULL COMMENT '执行次数',
  next_run_time BIGINT NOT NULL COMMENT '下次执行毫秒',
  lease_owner VARCHAR(128) NOT NULL COMMENT '租约持有者，空为无',
  lease_until BIGINT NOT NULL COMMENT '租约截止毫秒，0 为无',
  last_error VARCHAR(255) NOT NULL COMMENT '最后错误',
  create_time BIGINT NOT NULL COMMENT '创建时刻，Unix 毫秒',
  update_time BIGINT NOT NULL COMMENT '更新时刻，Unix 毫秒',
  PRIMARY KEY (id),
  UNIQUE KEY uk_job (job_key),
  KEY idx_pending (job_status,next_run_time,id),
  KEY idx_target (target_type,target_id,job_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='事务 outbox 与回调补偿';
