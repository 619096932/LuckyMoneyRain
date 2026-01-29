/*
 Navicat Premium Dump SQL

 Source Server         : 年会红包数据库
 Source Server Type    : MySQL
 Source Server Version : 80036 (8.0.36)
 Source Host           : localhost:3306
 Source Schema         : nianhui

 Target Server Type    : MySQL
 Target Server Version : 80036 (8.0.36)
 File Encoding         : 65001

 Date: 29/01/2026 15:42:05
*/

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table structure for award_batches
-- ----------------------------
DROP TABLE IF EXISTS `award_batches`;
CREATE TABLE `award_batches` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `round_id` bigint NOT NULL,
  `total_pool` bigint NOT NULL,
  `status` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `created_at` datetime NOT NULL,
  `confirmed_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_round` (`round_id`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for award_details
-- ----------------------------
DROP TABLE IF EXISTS `award_details`;
CREATE TABLE `award_details` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `batch_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `score` int NOT NULL,
  `amount` bigint NOT NULL,
  `created_at` datetime NOT NULL,
  `base_amount` bigint NOT NULL DEFAULT '0',
  `lucky_amount` bigint NOT NULL DEFAULT '0',
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_batch` (`batch_id`) USING BTREE,
  KEY `idx_user` (`user_id`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=605 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for click_events
-- ----------------------------
DROP TABLE IF EXISTS `click_events`;
CREATE TABLE `click_events` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `round_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `drop_id` int NOT NULL,
  `delta_score` int NOT NULL,
  `is_bomb` tinyint NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_round_user` (`round_id`,`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for round_whitelist
-- ----------------------------
DROP TABLE IF EXISTS `round_whitelist`;
CREATE TABLE `round_whitelist` (
  `round_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`round_id`,`user_id`) USING BTREE,
  KEY `idx_user` (`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for rounds
-- ----------------------------
DROP TABLE IF EXISTS `rounds`;
CREATE TABLE `rounds` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `title` varchar(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `total_pool` bigint NOT NULL,
  `duration_sec` int NOT NULL,
  `slice_ms` int NOT NULL,
  `drops_per_slice` int NOT NULL,
  `bombs_per_slice` int NOT NULL,
  `score_total` int NOT NULL,
  `bomb_penalty` int NOT NULL,
  `min_award` bigint NOT NULL DEFAULT '0',
  `max_award` bigint NOT NULL DEFAULT '0',
  `status` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `start_at_ms` bigint NOT NULL DEFAULT '0',
  `end_at_ms` bigint NOT NULL DEFAULT '0',
  `seed` int unsigned NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `bigs_per_slice` int NOT NULL DEFAULT '0',
  `empty_per_slice` int NOT NULL DEFAULT '0',
  `big_multiplier` double NOT NULL DEFAULT '2',
  `max_speed` double NOT NULL DEFAULT '1.2',
  `drop_visible_ms` int NOT NULL DEFAULT '0',
  `lucky_ratio` int NOT NULL DEFAULT '40',
  `base_ratio` int NOT NULL DEFAULT '60',
  `tail_top_n` int NOT NULL DEFAULT '3',
  `rank_segments` int NOT NULL DEFAULT '10',
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_status` (`status`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for scores
-- ----------------------------
DROP TABLE IF EXISTS `scores`;
CREATE TABLE `scores` (
  `round_id` bigint NOT NULL,
  `user_id` bigint NOT NULL,
  `score` int NOT NULL DEFAULT '0',
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`round_id`,`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for user_alipay_accounts
-- ----------------------------
DROP TABLE IF EXISTS `user_alipay_accounts`;
CREATE TABLE `user_alipay_accounts` (
  `user_id` bigint NOT NULL,
  `name` varchar(50) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `account` varchar(80) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `id_card` varchar(30) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for users
-- ----------------------------
DROP TABLE IF EXISTS `users`;
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `phone` varchar(20) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `nickname` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '',
  `avatar_url` varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '',
  `is_admin` tinyint NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`) USING BTREE,
  UNIQUE KEY `phone` (`phone`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=203 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for wallet_ledger
-- ----------------------------
DROP TABLE IF EXISTS `wallet_ledger`;
CREATE TABLE `wallet_ledger` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `amount` bigint NOT NULL,
  `reason` varchar(50) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `ref_id` bigint NOT NULL,
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_user` (`user_id`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=607 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for wallets
-- ----------------------------
DROP TABLE IF EXISTS `wallets`;
CREATE TABLE `wallets` (
  `user_id` bigint NOT NULL,
  `balance` bigint NOT NULL DEFAULT '0',
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`user_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

-- ----------------------------
-- Table structure for withdraw_requests
-- ----------------------------
DROP TABLE IF EXISTS `withdraw_requests`;
CREATE TABLE `withdraw_requests` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `amount` bigint NOT NULL,
  `status` varchar(20) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `alipay_name` varchar(50) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `alipay_account` varchar(80) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `id_card` varchar(30) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `paid_at` datetime DEFAULT NULL,
  `out_biz_no` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '',
  `auto_pay` tinyint NOT NULL DEFAULT '1',
  `attempts` int NOT NULL DEFAULT '0',
  `last_attempt_at` datetime DEFAULT NULL,
  `next_attempt_at` datetime DEFAULT NULL,
  `alipay_order_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_fund_order_id` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_status` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_code` varchar(16) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_msg` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_sub_code` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `alipay_sub_msg` varchar(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `fail_reason` varchar(256) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `raw_response` text CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci,
  PRIMARY KEY (`id`) USING BTREE,
  KEY `idx_user` (`user_id`) USING BTREE,
  KEY `idx_status` (`status`) USING BTREE,
  KEY `idx_out_biz` (`out_biz_no`) USING BTREE,
  KEY `idx_next_attempt` (`next_attempt_at`) USING BTREE,
  KEY `idx_auto_status` (`auto_pay`,`status`) USING BTREE
) ENGINE=InnoDB AUTO_INCREMENT=3 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;

SET FOREIGN_KEY_CHECKS = 1;
