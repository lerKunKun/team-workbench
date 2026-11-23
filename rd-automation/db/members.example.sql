-- Replace values before running this on an existing installation (see the repository README).
INSERT INTO app.user_identity_mapping
  (display_name, dingtalk_user_id, linear_user_id, github_user_id, modules, role)
VALUES ('真实姓名', '钉钉StaffId', 'Linear用户UUID', 'GitHub登录名', '后端、数据库', 'admin')
ON CONFLICT (dingtalk_user_id) DO UPDATE SET
  display_name = EXCLUDED.display_name, linear_user_id = EXCLUDED.linear_user_id,
  github_user_id = EXCLUDED.github_user_id, modules = EXCLUDED.modules, role = EXCLUDED.role,
  active = true;
