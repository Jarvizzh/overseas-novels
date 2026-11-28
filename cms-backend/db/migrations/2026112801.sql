-- Keep the check and all writes atomic, including when run directly with psql.
DO $initialize$
BEGIN
    PERFORM pg_advisory_xact_lock(2026111701);
    IF EXISTS (SELECT FROM public.reader_schema_migrations) THEN
        RAISE EXCEPTION 'Initial data requires an empty migration ledger; use CMS -migrate to upgrade';
    END IF;

    INSERT INTO system_configs (key, value) VALUES
        ('global_coin_cost_per_thousand', '500'),
        ('global_start_pay_chapter_index', '3')
    ON CONFLICT (key) DO NOTHING;

    -- Built-in system roles (idempotent).
    INSERT INTO roles (code, name, description, is_system, is_super_admin, status)
    VALUES
        ('Admin', '管理员', '系统内置超级管理员，拥有全部权限', TRUE, TRUE, 1),
        ('Leader', '负责人', '业务与运营负责人，拥有内容、投放及支付只读与商品管理权限', TRUE, FALSE, 1),
        ('AdOptimizer', '优化师', '负责推广链接、像素与回传中心投放管理', TRUE, FALSE, 1),
        ('ContentOperations', '内容运营', '负责书籍、书城配置、读者与反馈运营', TRUE, FALSE, 1),
        ('FinanceManager', '财务', '负责订单、订阅、退款、风控、争议、对账、商品及充值模板管理', TRUE, FALSE, 1)
    ON CONFLICT (code) DO NOTHING;

    -- Production role permissions verified on 2026-11-28.
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

    -- Runtime cutovers for Reader sessions, first-payment coverage and throughput reporting.
    INSERT INTO reader_schema_migrations (version) VALUES
        (2026111701), (2026111911), (2026111933), (2026112801), (2026112802)
    ON CONFLICT (version) DO NOTHING;
END
$initialize$;
