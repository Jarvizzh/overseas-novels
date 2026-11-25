-- RBAC single-role permission model, built-in roles, and authoritative SQL permission functions.

CREATE TABLE IF NOT EXISTS roles (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    is_super_admin BOOLEAN NOT NULL DEFAULT FALSE,
    status SMALLINT NOT NULL DEFAULT 1, -- 1-Active, 2-Disabled
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id BIGINT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_code VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (role_id, permission_code)
);

CREATE INDEX IF NOT EXISTS idx_role_permissions_code ON role_permissions(permission_code);

ALTER TABLE admins ALTER COLUMN role TYPE VARCHAR(64);
ALTER TABLE admins ADD COLUMN IF NOT EXISTS role_id BIGINT REFERENCES roles(id);
CREATE INDEX IF NOT EXISTS idx_admins_role_id ON admins(role_id);

-- Built-in system roles (idempotent).
INSERT INTO roles (code, name, description, is_system, is_super_admin, status)
VALUES
    ('Admin', '管理员', '系统内置超级管理员，拥有全部权限', TRUE, TRUE, 1),
    ('Leader', '负责人', '业务与运营负责人，拥有内容、投放及支付只读与商品管理权限', TRUE, FALSE, 1),
    ('AdOptimizer', '优化师', '负责推广链接、像素与回传中心投放管理', TRUE, FALSE, 1),
    ('ContentOperations', '内容运营', '负责书籍、书城配置、读者与反馈运营', TRUE, FALSE, 1),
    ('FinanceManager', '财务', '负责订单、订阅、退款、风控、争议、对账、商品及充值模板管理', TRUE, FALSE, 1)
ON CONFLICT (code) DO NOTHING;

-- Default permission matrix for built-in non-super-admin roles (§4.2).
WITH role_matrix(role_code, permission_code) AS (
    VALUES
        -- Leader (负责人)
        ('Leader', 'dashboard:read'),
        ('Leader', 'content.novels:read'),
        ('Leader', 'content.novels:write'),
        ('Leader', 'content.operations:read'),
        ('Leader', 'content.operations:write'),
        ('Leader', 'revenue.users:read'),
        ('Leader', 'revenue.users:write'),
        ('Leader', 'payments.orders:read'),
        ('Leader', 'payments.subscriptions:read'),
        ('Leader', 'payments.refunds:read'),
        ('Leader', 'payments.risk:read'),
        ('Leader', 'payments.disputes:read'),
        ('Leader', 'payments.reconciliation:read'),
        ('Leader', 'payments.products:read'),
        ('Leader', 'payments.products:write'),
        ('Leader', 'ads.promotions:read'),
        ('Leader', 'ads.promotions:write'),
        ('Leader', 'ads.templates:read'),
        ('Leader', 'ads.templates:write'),
        ('Leader', 'ads.pixels:read'),
        ('Leader', 'ads.pixels:write'),
        ('Leader', 'ads.conversions:read'),
        ('Leader', 'ads.conversions:write'),
        ('Leader', 'support.feedback:read'),
        ('Leader', 'support.feedback:write'),
        ('Leader', 'system.domains:read'),

        -- AdOptimizer (优化师)
        ('AdOptimizer', 'dashboard:read'),
        ('AdOptimizer', 'content.novels:read'),
        ('AdOptimizer', 'content.operations:read'),
        ('AdOptimizer', 'ads.promotions:read'),
        ('AdOptimizer', 'ads.promotions:write'),
        ('AdOptimizer', 'ads.templates:read'),
        ('AdOptimizer', 'ads.pixels:read'),
        ('AdOptimizer', 'ads.pixels:write'),
        ('AdOptimizer', 'ads.conversions:read'),
        ('AdOptimizer', 'ads.conversions:write'),

        -- ContentOperations (内容运营)
        ('ContentOperations', 'dashboard:read'),
        ('ContentOperations', 'content.novels:read'),
        ('ContentOperations', 'content.novels:write'),
        ('ContentOperations', 'content.operations:read'),
        ('ContentOperations', 'content.operations:write'),
        ('ContentOperations', 'revenue.users:read'),
        ('ContentOperations', 'revenue.users:write'),
        ('ContentOperations', 'ads.promotions:read'),
        ('ContentOperations', 'ads.promotions:write'),
        ('ContentOperations', 'ads.templates:read'),
        ('ContentOperations', 'support.feedback:read'),
        ('ContentOperations', 'support.feedback:write'),

        -- FinanceManager (财务)
        ('FinanceManager', 'dashboard:read'),
        ('FinanceManager', 'content.novels:read'),
        ('FinanceManager', 'revenue.users:read'),
        ('FinanceManager', 'payments.orders:read'),
        ('FinanceManager', 'payments.orders:write'),
        ('FinanceManager', 'payments.subscriptions:read'),
        ('FinanceManager', 'payments.subscriptions:write'),
        ('FinanceManager', 'payments.refunds:read'),
        ('FinanceManager', 'payments.refunds:write'),
        ('FinanceManager', 'payments.risk:read'),
        ('FinanceManager', 'payments.risk:write'),
        ('FinanceManager', 'payments.disputes:read'),
        ('FinanceManager', 'payments.disputes:write'),
        ('FinanceManager', 'payments.reconciliation:read'),
        ('FinanceManager', 'payments.reconciliation:write'),
        ('FinanceManager', 'payments.channels:read'),
        ('FinanceManager', 'payments.products:read'),
        ('FinanceManager', 'payments.products:write'),
        ('FinanceManager', 'ads.templates:read'),
        ('FinanceManager', 'ads.templates:write'),
        ('FinanceManager', 'ads.pixels:read'),
        ('FinanceManager', 'ads.conversions:read')
)
INSERT INTO role_permissions (role_id, permission_code)
SELECT r.id, m.permission_code
FROM role_matrix m
JOIN roles r ON r.code = m.role_code
ON CONFLICT (role_id, permission_code) DO NOTHING;

-- Empty-DB first release has no pre-existing admins; match only exact role codes if any row is present.
UPDATE admins a
SET role_id = r.id
FROM roles r
WHERE a.role_id IS NULL
  AND r.code = a.role;

ALTER TABLE admins ALTER COLUMN role_id SET NOT NULL;

-- 1. Authoritative permission check for Reader workers and general SQL queries.
CREATE OR REPLACE FUNCTION public.admin_has_permission(p_admin_id text, p_permission text)
RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = p_admin_id
          AND a.status = 1
          AND r.status = 1
          AND (
              r.is_super_admin = TRUE
              OR EXISTS (
                  SELECT 1
                  FROM public.role_permissions rp
                  WHERE rp.role_id = r.id
                    AND rp.permission_code = p_permission
              )
          )
    )
$$;

-- 2. Conversion write transaction lock: verifies active admin, active role, and required permission.
CREATE OR REPLACE FUNCTION conversion.lock_active_admin_for_permission(operator_id text, required_permission text)
RETURNS TABLE(role_code varchar)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    WITH locked AS (
        SELECT a.id, a.status AS admin_status, r.id AS role_id, r.code, r.status AS role_status, r.is_super_admin
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = operator_id
        FOR SHARE OF a, r
    )
    SELECT l.code::varchar
    FROM locked l
    WHERE l.admin_status = 1
      AND l.role_status = 1
      AND (
          l.is_super_admin = TRUE
          OR EXISTS (
              SELECT 1
              FROM public.role_permissions rp
              WHERE rp.role_id = l.role_id
                AND rp.permission_code = required_permission
          )
      )
$$;
REVOKE EXECUTE ON FUNCTION conversion.lock_active_admin_for_permission(text, text) FROM PUBLIC;

-- 3. Conversion retention lock: strictly limited to active super admins.
CREATE OR REPLACE FUNCTION conversion.lock_active_super_admin(operator_id text)
RETURNS TABLE(role_code varchar)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    WITH locked AS (
        SELECT a.status AS admin_status, r.code, r.status AS role_status, r.is_super_admin
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = operator_id
        FOR SHARE OF a, r
    )
    SELECT l.code::varchar
    FROM locked l
    WHERE l.admin_status = 1
      AND l.role_status = 1
      AND l.is_super_admin = TRUE
$$;
REVOKE EXECUTE ON FUNCTION conversion.lock_active_super_admin(text) FROM PUBLIC;

INSERT INTO reader_schema_migrations(version) VALUES (2026111935) ON CONFLICT (version) DO NOTHING;
