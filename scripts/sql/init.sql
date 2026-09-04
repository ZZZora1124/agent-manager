CREATE
DATABASE agent_runtime
CHARACTER SET utf8mb4
COLLATE utf8mb4_general_ci;

CREATE TABLE `cluster`
(
    `id`          int(10) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `name`        varchar(32)   NOT NULL DEFAULT '' COMMENT '集群名称',
    `kube_config` text          NOT NULL DEFAULT '' COMMENT 'k8s配置文件',
    `namespace`   varchar(32)   NOT NULL DEFAULT '' COMMENT 'namespace',
    `description` varchar(1024) NOT NULL DEFAULT '' COMMENT '描述',
    `create_time` datetime      NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='应用表，记录apiKey和资源账号的关系';

CREATE TABLE `app`
(
    `id`          int(10) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `api_key`     varchar(32)   NOT NULL DEFAULT '' COMMENT 'api key',
    `type`        varchar(32)   NOT NULL DEFAULT 'plat' COMMENT '类型:plat/platform,user',
    `account`     varchar(32)   NOT NULL DEFAULT '' COMMENT '资源账号ID',
    `owner`       varchar(64)   NOT NULL DEFAULT '' COMMENT '拥有者',
    `cluster`     varchar(32)   NOT NULL DEFAULT '' COMMENT '调度到哪个集群',
    `description` varchar(1024) NOT NULL DEFAULT '' COMMENT '描述',
    `create_time` datetime      NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`),
    KEY           `idx_api_key` (`api_key`),
    KEY           `idx_type` (`type`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='应用表，记录apiKey和资源账号的关系';


CREATE TABLE `app_grant_scope`
(
    `id`          int(10) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`      int(10) NOT NULL DEFAULT 0 COMMENT '所属应用',
    `type`        varchar(32) NOT NULL DEFAULT '' COMMENT '授权类型,如USER',
    `subject`     varchar(64) NOT NULL DEFAULT '' COMMENT 'USER类型时表示用户名',
    `create_time` datetime    NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='应用授权用户表';

CREATE TABLE `template`
(
    `id`          int(10) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`      int(10) NOT NULL DEFAULT 0 COMMENT '所属应用,0表示公共的模板',
    `name`        varchar(32)   NOT NULL DEFAULT '' COMMENT '模板唯一标识',
    `image`       varchar(256)  NOT NULL DEFAULT '' COMMENT '镜像路径，支持iRegistry',
    `memory`      int(10) NOT NULL DEFAULT 1024 COMMENT '内存大小，单位MB',
    `cpu`         int(10) NOT NULL DEFAULT 1 COMMENT 'cpu，单位毫核',
    `disk`        int(10) NOT NULL DEFAULT 10 COMMENT '磁盘大小，单位GB',
    `description` varchar(1024) NOT NULL DEFAULT '' COMMENT '描述',
    `create_time` datetime      NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='模板表';

CREATE TABLE `sandbox_runtime`
(
    `id`           bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`       int(10) NOT NULL DEFAULT 0 COMMENT '所属应用',
    `cluster`      varchar(32) NOT NULL DEFAULT '' COMMENT '所属集群',
    `template_id`  int(10) NOT NULL DEFAULT 0 COMMENT '所属模板',
    `sandbox_uuid` varchar(32) NOT NULL DEFAULT '' COMMENT '运行时唯一标识',
    `username`     varchar(64) NOT NULL DEFAULT '' COMMENT '所属用户',
    `port_ip`      varchar(32) NOT NULL DEFAULT '' COMMENT '运行时pod ip地址',
    `node_ip`      varchar(32) NOT NULL DEFAULT '' COMMENT '运行时node ip地址',
    `command_port` int(10) NOT NULL DEFAULT 0 COMMENT '执行命令的端口号',
    `code_port`    int(10) NOT NULL DEFAULT 0 COMMENT '执行的端口号',
    `token`        varchar(32) NOT NULL DEFAULT '' COMMENT '认证token',
    `status`       varchar(32) NOT NULL DEFAULT 'init' COMMENT '运行状态，init、running、fail、success',
    `create_time`  datetime    NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `end_time`     datetime    NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '结束时间',
    `expire_time`  datetime NULL COMMENT '过期时间',
    `metadata`     text COMMENT '运行时元数据',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='运行时实例表';

-- 修改sandbox_runtime主键id类型
ALTER TABLE sandbox_runtime MODIFY COLUMN id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT;

-- 添加启动时间字段
ALTER TABLE sandbox_runtime
    ADD COLUMN start_time DATETIME NULL COMMENT '启动时间' AFTER create_time;

-- 添加过期时间字段
ALTER TABLE sandbox_runtime
    ADD COLUMN timeout int(10) DEFAULT 0 COMMENT '过期时间' AFTER end_time;

-- 添加索引,用户根据状态过滤
ALTER TABLE sandbox_runtime
    ADD KEY `status_app_id_idx` (`status`,`app_id`);

-- 为app表添加type字段
ALTER TABLE app
    ADD COLUMN type VARCHAR(32) NOT NULL DEFAULT 'plat' COMMENT '类型:plat/user' AFTER api_key;

-- 为app表添加limit_cnt字段
ALTER TABLE app
    ADD COLUMN limit_cnt bigint(10) NOT NULL DEFAULT 1 COMMENT '限制的沙箱个数' AFTER `type`;

-- 为app表添加历史最高并发字段
ALTER TABLE app
    ADD COLUMN max_occupancy_cnt bigint(10) NOT NULL DEFAULT 0 COMMENT '历史最高并发数' AFTER `limit_cnt`;

-- 镜像预热任务表
CREATE TABLE `image_preheat_job`
(
    `id`              bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`          int(10) NOT NULL DEFAULT 0 COMMENT '所属应用',
    `job_name`        varchar(64)  NOT NULL DEFAULT '' COMMENT 'job名称',
    `image_url`       varchar(256) NOT NULL DEFAULT '' COMMENT '镜像地址',
    `registry_secret` varchar(64)  NOT NULL DEFAULT '' COMMENT '镜像仓库secret名称',
    `cluster_list`    text COMMENT '目标集群列表，JSON格式',
    `node_selector`   text COMMENT 'node选择器，JSON格式',
    `max_concurrency` int(10) NOT NULL DEFAULT 5 COMMENT '最大并发数',
    `status`          varchar(32)  NOT NULL DEFAULT 'pending' COMMENT '任务状态：pending,running,success,failed',
    `total_nodes`     int(10) NOT NULL DEFAULT 0 COMMENT '总节点数',
    `success_nodes`   int(10) NOT NULL DEFAULT 0 COMMENT '成功节点数',
    `failed_nodes`    int(10) NOT NULL DEFAULT 0 COMMENT '失败节点数',
    `create_time`     datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `start_time`      datetime NULL COMMENT '开始时间',
    `end_time`        datetime NULL COMMENT '结束时间',
    PRIMARY KEY (`id`),
    KEY               `idx_app_id` (`app_id`),
    KEY               `idx_status` (`status`),
    KEY               `idx_create_time` (`create_time`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='镜像预热任务表';

-- 镜像预热记录表（每个节点）
CREATE TABLE `image_preheat_record`
(
    `id`           bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `job_id`       bigint(20) NOT NULL DEFAULT 0 COMMENT '所属任务ID',
    `job_name`     varchar(64)  NOT NULL DEFAULT '' COMMENT 'job名称',
    `cluster_name` varchar(64)  NOT NULL DEFAULT '' COMMENT '集群名称',
    `node_name`    varchar(64)  NOT NULL DEFAULT '' COMMENT '节点名称',
    `node_ip`      varchar(32)  NOT NULL DEFAULT '' COMMENT '节点IP',
    `pod_name`     varchar(64)  NOT NULL DEFAULT '' COMMENT 'pod名称',
    `image_url`    varchar(256) NOT NULL DEFAULT '' COMMENT '镜像地址',
    `status`       varchar(32)  NOT NULL DEFAULT 'pending' COMMENT '预热状态：pending,running,success,failed',
    `error_msg`    text COMMENT '错误信息',
    `start_time`   datetime NULL COMMENT '开始时间',
    `end_time`     datetime NULL COMMENT '结束时间',
    `create_time`  datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`),
    KEY            `idx_job_id` (`job_id`),
    KEY            `idx_cluster_node` (`cluster_name`,`node_name`),
    KEY            `idx_status` (`status`),
    KEY            `idx_create_time` (`create_time`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='镜像预热记录表';

-- 添加app_pvc_binding表用于维护app与PVC的绑定关系
CREATE TABLE `app_pvc_binding`
(
    `id`          bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`      int(10) NOT NULL DEFAULT 0 COMMENT '所属应用ID',
    `pvc_name`    varchar(128) NOT NULL DEFAULT '' COMMENT 'PVC名称',
    `mount_path`  varchar(256) NOT NULL DEFAULT '' COMMENT '挂载路径',
    `create_time` datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `update_time` datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    KEY           `idx_app_id` (`app_id`),
    KEY           `idx_pvc_name` (`pvc_name`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='app与PVC绑定关系表';

-- 按sandbox_uuid维度维护PVC绑定关系
CREATE TABLE `sandbox_pvc_binding`
(
    `id`            bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `sandbox_uuid`  varchar(32)  NOT NULL DEFAULT '' COMMENT '沙箱唯一ID',
    `app_id`        int(10) NOT NULL DEFAULT 0 COMMENT '所属应用ID',
    `pvc_name`      varchar(128) NOT NULL DEFAULT '' COMMENT 'PVC名称',
    `mount_path`    varchar(256) NOT NULL DEFAULT '' COMMENT '挂载路径',
    `cluster`       varchar(64)  NOT NULL DEFAULT '' COMMENT '所属集群',
    `namespace`     varchar(128) NOT NULL DEFAULT '' COMMENT '所属命名空间',
    `status`        varchar(32)  NOT NULL DEFAULT 'active' COMMENT '绑定状态:active/released/deleting/deleted/failed',
    `reclaim_after` datetime              DEFAULT NULL COMMENT '可回收时间',
    `deleted`       tinyint(1) NOT NULL DEFAULT '0' COMMENT '删除标识：0-未删除, 1-已删除',
    `create_time`   datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `update_time`   datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_sandbox_uuid_deleted` (`sandbox_uuid`,`deleted`),
    UNIQUE KEY `uk_pvc_name_deleted` (`pvc_name`,`deleted`),
    KEY             `idx_status_reclaim_after` (`status`,`reclaim_after`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8mb4 COMMENT='sandbox与PVC绑定关系表';

-- 添加日志UUID字段
ALTER TABLE sandbox_runtime
    ADD COLUMN log_uuid varchar(64) NOT NULL default '' COMMENT '日志唯一ID' AFTER metadata;


-- 新增镜像表
CREATE TABLE `image`
(
    `id`        int(10) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `name`      varchar(32)  NOT NULL DEFAULT '' COMMENT '镜像名称',
    `type`      varchar(32)  NOT NULL DEFAULT '' COMMENT '镜像类别',
    `image_url` varchar(256) NOT NULL DEFAULT '' COMMENT '镜像路径，支持iRegistry',
    `status`    varchar(32)  NOT NULL DEFAULT '' COMMENT '镜像状态',
    `scope`     varchar(32)  NOT NULL DEFAULT '' COMMENT '镜像范围: public为公共镜像，custom为自定义镜像',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=34 DEFAULT CHARSET=utf8mb4 COMMENT='镜像表'

ALTER TABLE app
    ADD COLUMN worksapce_id int(10) NOT NULL DEFAULT 0 COMMENT '所属工作空间';
ALTER TABLE app
    ADD COLUMN `workspace_name` varchar(64) NOT NULL DEFAULT '' COMMENT '空间名称';

ALTER TABLE template
    ADD COLUMN type varchar(32) NOT NULL DEFAULT '' COMMENT '沙箱类别',
    ADD COLUMN envir_vars text COMMENT '环境变量',
    ADD COLUMN image_id int(10) NOT NULL DEFAULT 0 COMMENT '镜像id',
    ADD COLUMN res_type varchar(32) NOT NULL DEFAULT '' COMMENT '资源类型：基础套餐，标准套餐，高级套餐和自定义套餐',
    ADD COLUMN icode tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否拥有icode权限 0-无 1-有',
    ADD COLUMN ducc tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否拥有ducc权限 0-无 1-有';

ALTER TABLE sandbox_runtime
    ADD COLUMN name varchar(32) NOT NULL DEFAULT '' COMMENT '实例名称',
    ADD COLUMN create_user varchar(32) NOT NULL DEFAULT '' COMMENT '创建用户',
    ADD COLUMN memory int(10) NOT NULL DEFAULT 1024 COMMENT '内存大小，单位MB',
    ADD COLUMN cpu int(10) NOT NULL DEFAULT 1 COMMENT 'cpu，单位毫核',
    ADD COLUMN disk int(10) NOT NULL DEFAULT 10 COMMENT '磁盘大小，单位GB',
    ADD COLUMN release_name varchar(32) NOT NULL DEFAULT '' COMMENT '释放人',
    ADD COLUMN envir_vars text COMMENT '环境变量',
    ADD COLUMN image_id int(10) NOT NULL DEFAULT 0 COMMENT '镜像id',
    ADD COLUMN icode tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否拥有icode权限 0-无 1-有',
    ADD COLUMN ducc tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否拥有ducc权限 0-无 1-有';

alter table template
    add column `deleted` tinyint(1) NOT NULL DEFAULT 0 COMMENT '删除标识：0-未删除, 1-已删除';

alter table sandbox_runtime
    add column `deleted` tinyint(1) NOT NULL DEFAULT 0 COMMENT '删除标识：0-未删除, 1-已删除';

alter table template
    add column create_user varchar(32) NOT NULL DEFAULT '' COMMENT '创建用户';

alter table template
    add column update_user varchar(32) NOT NULL DEFAULT '' COMMENT '更新用户';

alter table template
    add column update_time datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '更新时间';

alter table sandbox_runtime
    add column remind tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否开启通知标识：0-不开启, 1-开启';

alter table sandbox_runtime
    add column period_time int(10) DEFAULT '0' COMMENT '过期前通知时间';

alter table sandbox_runtime
    add column is_reminded tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否已经提醒：0-未提醒, 1-已提醒';

alter table sandbox_runtime
    add column failed_reason varchar(64) NOT NULL DEFAULT '' COMMENT '实例创建失败原因',

ALTER TABLE sandbox_runtime
    MODIFY COLUMN failed_reason VARCHAR (512)
    NOT NULL DEFAULT '' COMMENT '实例创建失败原因';

-- 新增镜像表
CREATE TABLE `artifacts`
(
    `id`          bigint(20) unsigned NOT NULL AUTO_INCREMENT COMMENT '自增主键id',
    `app_id`      int(10) NOT NULL DEFAULT 1 COMMENT '平台ID',
    `sandbox_id`  varchar(32)  NOT NULL DEFAULT '' COMMENT '沙箱实例ID',
    `scope`       varchar(32)  NOT NULL DEFAULT '' COMMENT '作用范围,PUBLIC/APP/SANDBOX',
    `artifact_id` varchar(256) NOT NULL DEFAULT '' COMMENT '制品唯一标识',
    `size`        bigint(20) NOT NULL DEFAULT 1 COMMENT '制品大小',
    `object_key`  varchar(256) NOT NULL DEFAULT '' COMMENT '对象存储key',
    `password`    varchar(128) NOT NULL DEFAULT '' COMMENT '密码',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=34 DEFAULT CHARSET=utf8mb4 COMMENT='制品表';

alter table template
    add column code_bean varchar(128) NOT NULL DEFAULT '' COMMENT '代码库/分支等';

alter table template
    add column post_script text CHARACTER SET utf8mb4 COMMENT '启动命令';

alter table sandbox_runtime
    add column code_bean varchar(128) NOT NULL DEFAULT '' COMMENT '代码库/分支等';

alter table sandbox_runtime
    add column post_script text CHARACTER SET utf8mb4 COMMENT '启动命令';

alter table sandbox_runtime
    add column pod_name varchar(256) NOT NULL DEFAULT '' COMMENT 'pod名称';


alter table sandbox_runtime
    add column `module` varchar(256) NOT NULL DEFAULT '' COMMENT 'iCode模块名';

CREATE TABLE `sandbox_runtime_pool`
(
    `id`            bigint(20) unsigned NOT NULL AUTO_INCREMENT,
    `app_id`        int(10) NOT NULL DEFAULT '0' COMMENT '所属应用',
    `template_id`   int(10) NOT NULL DEFAULT '0' COMMENT '所属模板',
    `cluster`       varchar(32)  NOT NULL DEFAULT '' COMMENT '所属集群',
    `sandbox_uuid`  varchar(32)  NOT NULL DEFAULT '' COMMENT '运行时唯一标识',
    `port_ip`       varchar(32)  NOT NULL DEFAULT '' COMMENT '运行时pod ip地址',
    `node_ip`       varchar(32)  NOT NULL DEFAULT '' COMMENT '运行时node ip地址',
    `command_port`  int(10) NOT NULL DEFAULT '0' COMMENT '执行命令的端口号',
    `code_port`     int(10) NOT NULL DEFAULT '0' COMMENT '执行的端口号',
    `token`         varchar(32)  NOT NULL DEFAULT '' COMMENT '认证token',
    `status`        varchar(32)  NOT NULL DEFAULT 'init' COMMENT '运行状态，init、running、fail、success',
    `create_time`   datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `start_time`    datetime              DEFAULT NULL COMMENT '启动时间',
    `use_time`      datetime              DEFAULT NULL COMMENT '使用时间',
    `metadata`      text COMMENT '运行时元数据',
    `log_uuid`      varchar(64)  NOT NULL DEFAULT '' COMMENT '日志唯一ID',
    `name`          varchar(32)  NOT NULL DEFAULT '' COMMENT '实例名称',
    `create_user`   varchar(32)  NOT NULL DEFAULT '' COMMENT '创建用户',
    `memory`        int(10) NOT NULL DEFAULT '1024' COMMENT '内存大小，单位MB',
    `cpu`           int(10) NOT NULL DEFAULT '1' COMMENT 'cpu，单位毫核',
    `disk`          int(10) NOT NULL DEFAULT '10' COMMENT '磁盘大小，单位GB',
    `envir_vars`    text COMMENT '环境变量',
    `image_id`      int(10) NOT NULL DEFAULT '0' COMMENT '镜像id',
    `failed_reason` varchar(512) NOT NULL DEFAULT '' COMMENT '实例创建失败原因',
    `code_bean`     varchar(128) NOT NULL DEFAULT '' COMMENT '代码库/分支等',
    `post_script`   text CHARACTER SET utf8mb4 COMMENT '启动命令',
    `pod_name`      varchar(256) NOT NULL DEFAULT '' COMMENT 'pod名称',
    PRIMARY KEY (`id`),
    KEY             `status_app_id_idx` (`status`,`app_id`)
) ENGINE=InnoDB AUTO_INCREMENT=1 DEFAULT CHARSET=utf8 COMMENT='预热实例表';

alter table template
    add column preheat_count int(10) NOT NULL DEFAULT 0  COMMENT '预热个数';

alter table sandbox_runtime
    add column `use_pvc` tinyint(1) NOT NULL DEFAULT '0' COMMENT '是否开启PVC持久化';

CREATE TABLE `sandbox_pvc_migration`
(
    `id`              bigint(20) unsigned NOT NULL AUTO_INCREMENT,
    `sandbox_uuid`    varchar(64)  NOT NULL DEFAULT '' COMMENT '沙箱UUID',
    `binding_id`      bigint(20) NOT NULL DEFAULT 0 COMMENT '关联的PVC binding ID',
    `source_node`     varchar(255) NOT NULL DEFAULT '' COMMENT '源节点',
    `target_node`     varchar(255) NOT NULL DEFAULT '' COMMENT '目标节点',
    `source_pvc_name` varchar(128) NOT NULL DEFAULT '' COMMENT '源PVC名',
    `source_pv_name`  varchar(128) NOT NULL DEFAULT '' COMMENT '源PV名',
    `source_pv_path`  varchar(512) NOT NULL DEFAULT '' COMMENT '源PV路径',
    `cluster`         varchar(64)  NOT NULL DEFAULT '' COMMENT '集群名',
    `namespace`       varchar(128) NOT NULL DEFAULT '' COMMENT '命名空间',
    `status`          varchar(32)  NOT NULL DEFAULT 'pending' COMMENT '状态:pending/running/done/failed',
    `error_msg`       text COMMENT '错误信息',
    `create_time`     datetime     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `start_time`      datetime              DEFAULT NULL,
    `end_time`        datetime              DEFAULT NULL,
    PRIMARY KEY (`id`),
    KEY               `idx_sandbox_uuid` (`sandbox_uuid`),
    KEY               `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='PVC同集群迁移记录';

ALTER TABLE cluster
    ADD COLUMN network_type VARCHAR(128) NOT NULL DEFAULT '' COMMENT '网络类型,vpc/vpc-eni';
ALTER TABLE cluster
    ADD COLUMN nginx_endpoint VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'nginx服务地址';

alter table sandbox_pvc_binding
    add column `storage_class` varchar(64) NOT NULL DEFAULT 'local-path' COMMENT '存储类: local-path, hp1, sharedcfs, csi-bos2';

-- sandbox_pvc_migration 表新增跨存储类迁移字段
ALTER TABLE sandbox_pvc_migration
    ADD COLUMN `migration_type` VARCHAR(32) NOT NULL DEFAULT 'cross_node'
        COMMENT '迁移类型: cross_node(跨节点), cross_storage(跨存储类)',
    ADD COLUMN `target_storage_class` VARCHAR(64) NOT NULL DEFAULT ''
        COMMENT '目标存储类(cross_storage 时使用)',
    ADD COLUMN `target_pvc_name` VARCHAR(128) NOT NULL DEFAULT ''
        COMMENT '目标PVC名称(cross_storage 时使用)';

-- 沙箱运行时操作历史表
CREATE TABLE `sandbox_runtime_history`
(
    `sandbox_runtime_history_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
    `sandbox_uuid`              varchar(32)  NOT NULL DEFAULT '' COMMENT '沙箱唯一标识',
    `app_id`                   int(10)      NOT NULL DEFAULT 0 COMMENT '所属应用',
    `template_id`              int(10)      NOT NULL DEFAULT 0 COMMENT '所属模板',
    `name`                     varchar(32)  NOT NULL DEFAULT '' COMMENT '实例名称',
    `cluster`                  varchar(32)  NOT NULL DEFAULT '' COMMENT '所属集群',
    `create_user`              varchar(64)  NOT NULL DEFAULT '' COMMENT '创建用户',
    `delete_user`              varchar(64)  NOT NULL DEFAULT '' COMMENT '删除用户',
    `start_time`               datetime     NOT NULL COMMENT '开始时间',
    `end_time`                 datetime     NOT NULL COMMENT '结束时间',
    `status`                   varchar(32)  NOT NULL DEFAULT '' COMMENT '释放后状态: user_kill/idle_kill/expire_kill',
    `node_ip`                  varchar(32)  NOT NULL DEFAULT '' COMMENT '节点IP',
    `pod_ip`                   varchar(32)  NOT NULL DEFAULT '' COMMENT 'Pod IP',
    `error_msg`                varchar(512) NOT NULL DEFAULT '' COMMENT '失败原因（如有）',
    PRIMARY KEY (`sandbox_runtime_history_id`),
    KEY                        `idx_sandbox_uuid` (`sandbox_uuid`),
    KEY                        `idx_app_id` (`app_id`),
    KEY                        `idx_start_time` (`start_time`),
    KEY                        `idx_end_time` (`end_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='沙箱运行时操作历史表';

CREATE TABLE host_sequence
(
    id              BIGINT PRIMARY KEY AUTO_INCREMENT,
    current_host_id BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMP       DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP       DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

INSERT INTO host_sequence (current_host_id) VALUES (910100);

CREATE TABLE cluster_node
(
    id             BIGINT PRIMARY KEY AUTO_INCREMENT,
    host_id        BIGINT      NOT NULL UNIQUE COMMENT '节点唯一标识',
    cluster_id     VARCHAR(64) NOT NULL COMMENT '集群ID',
    instance_id    VARCHAR(64) NOT NULL COMMENT '云实例ID',
    instance_name  VARCHAR(128) COMMENT '实例名称',
    hostname       VARCHAR(128) COMMENT '主机名',
    bns_name       VARCHAR(128) COMMENT '绑定的BNS',
    ip             VARCHAR(64) COMMENT '内网IP',
    status         VARCHAR(32) NOT NULL DEFAULT 'creating' COMMENT 'creating/running/ready',
    instance_phase VARCHAR(32) COMMENT '云平台返回的实例状态',
    created_at     TIMESTAMP            DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP            DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX          idx_cluster_id (cluster_id),
    INDEX          idx_status (status),
    INDEX          idx_host_id (host_id)
) COMMENT='集群节点表';

-- 为 sandbox_runtime 和 sandbox_runtime_pool 的 sandbox_uuid 列添加唯一索引，作为去重兜底
ALTER TABLE sandbox_runtime ADD UNIQUE KEY `uk_sandbox_uuid` (`sandbox_uuid`);
ALTER TABLE sandbox_runtime_pool ADD UNIQUE KEY `uk_sandbox_uuid` (`sandbox_uuid`);
ALTER TABLE sandbox_runtime_pool ADD KEY `template_id_status` (`template_id`,`status`);


