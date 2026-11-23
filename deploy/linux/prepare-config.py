#!/usr/bin/env python3
"""Validate config.yaml and derive Compose/container environment files (no services started)."""
import argparse
from base64 import b64decode
import json
import os
from pathlib import Path
import re
import sys
import tempfile
from urllib.parse import unquote, urlsplit

try:
    import yaml
except ImportError:
    sys.exit('需要 PyYAML：python3 -m pip install PyYAML（建议使用虚拟环境）')


DEFAULT_DEV_JWT_SECRETS = {
    'super-secret-key-star-novel-2026',
    'super-secret-key-star-novel-cms-2026',
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='仅校验 config.yaml，不生成环境文件')
    root = Path(__file__).resolve().parent
    parser.add_argument('--config', type=Path, default=root / 'config.yaml', help='配置文件路径')
    parser.add_argument('--output-dir', type=Path, help='生成环境文件的目录，测试配置必须指定')
    args = parser.parse_args()
    output = args.output_dir or root

    class UniqueLoader(yaml.SafeLoader):
        pass

    def mapping(loader, node):
        result = {}
        for k, v in node.value:
            key = loader.construct_object(k)
            if key in result:
                raise ValueError('配置包含重复键')
            result[key] = loader.construct_object(v)
        return result

    UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, mapping)
    try:
        c = yaml.load(args.config.read_text(encoding='utf-8'), Loader=UniqueLoader)
        if not isinstance(c, dict):
            raise ValueError()
    except Exception:
        sys.exit('config.yaml 无法解析或存在重复键；请本地检查，未输出文件内容。')

    def get(path, default=''):
        value = c
        for key in path.split('.'):
            value = value.get(key, {}) if isinstance(value, dict) else {}
        return default if value == {} or value is None else value

    problems = []

    def is_placeholder(val: str) -> bool:
        upper = val.upper()
        return any(p in upper for p in ('YOUR_', 'CHANGE_ME', 'REPLACE_', '<填写', '<填入'))

    def write_private(filename: str, content: str):
        fd, temp = tempfile.mkstemp(dir=output, prefix='.prepare-')
        try:
            with os.fdopen(fd, 'w', encoding='utf-8') as f:
                f.write(content)
            os.chmod(temp, 0o600)
            os.replace(temp, output / filename)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)

    def write_env(filename: str, data: dict):
        lines = [f'# Generated from {args.config.name}.']
        for key, value in data.items():
            escaped = value.replace("'", "\\'")
            lines.append(f"{key}='{escaped}'")
        write_private(filename, '\n'.join(lines) + '\n')

    def generate(files):
        values = (value for data in files.values() for value in data.values())
        if any('\n' in v or '\r' in v or '\x00' in v for v in values):
            problems.append('配置值不能包含换行符或空字符')
        if problems:
            print('需要补充或修正（不显示配置值）：', file=sys.stderr)
            for item in problems:
                print('- ' + item, file=sys.stderr)
            return 1
        if args.check:
            print('配置校验通过；未修改生成文件。')
            return 0
        output.mkdir(parents=True, exist_ok=True)
        for filename, data in files.items():
            write_env(filename, data)
        print('已生成服务环境文件（权限 600），未启动或修改任何服务。')
        return 0

    def payment_files():
        def string(name, default=''):
            value = get(f'payment.{name}', default)
            if not isinstance(value, str):
                problems.append(f'payment.{name}：须为字符串')
                return default
            return value.strip()

        def boolean(name, default):
            value = get(f'payment.{name}', default)
            if not isinstance(value, bool):
                problems.append(f'payment.{name}：须为布尔值')
                return str(default).lower()
            return str(value).lower()

        keyring = string('credential_keys')
        active = string('credential_active_key_version')
        versions = set()
        if not keyring:
            problems.append('payment.credential_keys：须配置版本化的 32 字节 Base64 加密密钥')
        else:
            for entry in keyring.split(','):
                version, separator, key = (part.strip() for part in entry.partition(':'))
                try:
                    decoded = b64decode(key + '=' * (-len(key) % 4), validate=True)
                except ValueError:
                    decoded = b''
                if not separator or not version or version in versions or len(decoded) != 32:
                    problems.append('payment.credential_keys：每项须为唯一的 version:base64，密钥解码后须为 32 字节')
                    break
                versions.add(version)
        if not active or active not in versions:
            problems.append('payment.credential_active_key_version：须指定密钥环中已有的版本')

        common = {
            'PAYMENT_CREDENTIAL_KEYS': keyring,
            **{name.upper(): string(name) for name in (
                'paypal_webhook_id',
                'stripe_secret_key', 'stripe_webhook_secret',
            )},
        }
        cms = {
            'PAYMENT_CREDENTIAL_ACTIVE_KEY_VERSION': active,
            'STRIPE_CATALOG_LIVE_API_KEY': string('stripe_catalog_live_api_key'),
            'STRIPE_CATALOG_SANDBOX_API_KEY': string('stripe_catalog_sandbox_api_key'),
        }
        reader = {
            'PAYMENT_TASK_METRICS_ENABLED': boolean('task_metrics_enabled', True),
            'STRIPE_SINGLE_CHECKOUT_ENABLED': boolean('stripe_single_checkout_enabled', False),
            'STRIPE_VIP_CHECKOUT_ENABLED': boolean('stripe_vip_checkout_enabled', False),
            **{name.upper(): string(name) for name in (
                'paypal_merchant_id', 'stripe_ledger_api_key', 'stripe_aftercare_api_key',
                'stripe_aftercare_account', 'stripe_aftercare_environment', 'stripe_aftercare_webhook_secret',
            )},
        }
        refs = get('payment.account_references', [])
        if not isinstance(refs, list) or any(not isinstance(ref, dict) for ref in refs):
            problems.append('payment.account_references：须为账户引用对象列表')
            refs = []
        reader['PAYMENT_ACCOUNT_REFERENCES'] = json.dumps(refs, ensure_ascii=False)

        extra = get('payment.account_environment', {})
        if not isinstance(extra, dict):
            problems.append('payment.account_environment：须为被引用的环境变量和值的映射')
            extra = {}
        referenced = set()
        for ref in refs:
            for field in ('secret_env', 'webhook_env', 'merchant_env', 'ledger_env'):
                name = ref.get(field, '')
                if not isinstance(name, str) or (name and not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', name)):
                    problems.append(f'payment.account_references.{field}：须为有效环境变量名')
                elif name:
                    referenced.add(name)
        if extra.keys() - referenced or any(not isinstance(value, str) for value in extra.values()):
            problems.append('payment.account_environment：仅允许账户引用中的变量，值须为字符串')
            extra = {}
        return common, cms, reader, extra

    def conversion_files(dsn):
        def read(name, default=''):
            return get(f'conversion.{name}', default)

        def string(name, default=''):
            return str(read(name, default)).strip()

        def boolean(name, default=False):
            value = read(name, default)
            if not isinstance(value, bool):
                problems.append(f'conversion.{name}：须为布尔值')
                return default
            return value

        def integer(name, default, upper):
            value = read(name, default)
            if type(value) is not int or not 1 <= value <= upper:
                problems.append(f'conversion.{name}：须为 1–{upper} 的整数')
                return default
            return value

        def interval(name, default):
            value = string(name, default)
            match = re.fullmatch(r'(\d+)(ms|s)', value)
            if not match or not 100 <= int(match[1]) * (1 if match[2] == 'ms' else 1000) <= 30000:
                problems.append(f'conversion.{name}：须为 100ms–30s 的整数时长')
            return value

        environment = string('environment', 'production')
        private_url = string('private_url', 'http://conversion-api:8092')
        private_token = string('private_token')
        key_version = string('credential_key_version', 'v1')
        keys_json = string('credential_keys_json')
        mock_enabled = boolean('sinan_mock_enabled')
        report_enabled = boolean('partner_report_enabled')
        meta_enabled = boolean('meta_enabled')
        graph_version = string('meta_graph_version', 'v21.0')
        private_addr = string('private_addr', ':8092')
        callback_addr = string('callback_addr', '127.0.0.1:8093')
        def address_port(name, value, loopback_only=False):
            host, sep, port = value.rpartition(':')
            if (not sep or not port.isascii() or not port.isdecimal() or not 1 <= int(port) <= 65535
                    or (loopback_only and host not in ('127.0.0.1', '[::1]'))):
                problems.append(f'conversion.{name}：监听地址无效')
                return 8092
            return int(port)
        private_port = address_port('private_addr', private_addr)
        address_port('callback_addr', callback_addr, loopback_only=True)
        host_port = integer('private_host_port', private_port, 65535)
        workers = integer('worker_instances', 2, 32)
        pool = integer('worker_db_max_conns', 16, 128)
        other_pool = integer('db_max_conns', 8, 128)
        global_limit = integer('meta_global_limit', 5, 100)
        concurrency = integer('meta_concurrency', min(5, global_limit), global_limit)
        reader = integer('reader_normalizers', 2, 3)
        inbox = integer('inbox_normalizers', 2, 3)
        budget = integer('normalizer_limit', min(4, max(1, pool // 2)), max(1, pool // 2))
        if pool < 2 or reader + (inbox if mock_enabled else 0) > budget:
            problems.append('conversion.normalizer_limit：启用的消费者须落在连接预算内')
        if mock_enabled and environment != 'test':
            problems.append('conversion.sinan_mock_enabled：仅允许 test')
        if report_enabled and not mock_enabled:
            problems.append('conversion.partner_report_enabled：需要 test 司南模拟接入')
        if not re.fullmatch(r'v[0-9]+\.[0-9]+', graph_version):
            problems.append('conversion.meta_graph_version：需要 vNN.0 格式')
        if not private_url or is_placeholder(private_url) or urlsplit(private_url).scheme not in ('http', 'https'):
            problems.append('conversion.private_url：必须填写私网 API 地址')
        if len(private_token) < 32 or is_placeholder(private_token):
            problems.append('conversion.private_token：必须设置至少 32 字符的共享 Token')
        try:
            keys = json.loads(keys_json)
            if not isinstance(keys, dict) or key_version not in keys or any(
                not isinstance(key, str) or len(b64decode(key, validate=True)) != 32 for key in keys.values()
            ):
                raise ValueError()
        except (ValueError, TypeError):
            problems.append('conversion.credential_keys_json：需要包含当前版本的 32 字节 Base64 密钥映射')

        def base(mode, max_conns=other_pool):
            value = string(f'{mode.replace("-", "_")}_database_url', dsn)
            try:
                role_db = urlsplit(value)
                if not role_db.hostname or role_db.scheme not in ('postgres', 'postgresql'):
                    raise ValueError()
                if environment == 'test' and role_db.hostname not in ('localhost', '127.0.0.1', '::1', 'host.docker.internal', 'postgres'):
                    raise ValueError()
            except ValueError:
                problems.append(f'conversion.{mode}：数据库 URL 无效，测试使用本地隔离库')
            return {
                'DATABASE_URL': value,
                'CONVERSION_ENVIRONMENT': environment,
                'CONVERSION_DB_MAX_CONNS': str(max_conns),
            }

        mock = {'CONVERSION_SINAN_MOCK_ENABLED': str(mock_enabled).lower()}
        files = {
            'conversion-api.env': {
                **base('api'),
                'CONVERSION_PRIVATE_ADDR': private_addr,
                'CONVERSION_PRIVATE_TOKEN': private_token,
                'CONVERSION_CREDENTIAL_KEY_VERSION': key_version,
                'CONVERSION_CREDENTIAL_KEYS': keys_json,
            },
            'conversion-worker.env': {
                **base('worker'), **mock,
                'CONVERSION_WORKER_DB_MAX_CONNS': str(pool),
                'CONVERSION_INSTANCE_ID': '',
                'CONVERSION_META_ENABLED': str(meta_enabled).lower(),
                'META_GRAPH_VERSION': graph_version,
                'CONVERSION_CREDENTIAL_KEYS': keys_json,
                'CONVERSION_META_BATCH_SIZE': str(integer('meta_batch_size', 1, 100)),
                'CONVERSION_META_GLOBAL_LIMIT': str(global_limit),
                'CONVERSION_META_CONCURRENCY': str(concurrency),
                'CONVERSION_META_PIXEL_INTERVAL': interval('meta_pixel_interval', '3s'),
                'CONVERSION_WORKER_POLL_INTERVAL': interval('worker_poll_interval', '15s'),
                'CONVERSION_WORKER_QUEUE_PROBE_INTERVAL': interval('worker_queue_probe_interval', '250ms'),
                'CONVERSION_META_REQUEST_TIMEOUT': interval('meta_request_timeout', '5s'),
                'CONVERSION_NORMALIZE_BATCH': str(integer('normalize_batch', 20, 500)),
                'CONVERSION_READER_NORMALIZERS': str(reader),
                'CONVERSION_INBOX_NORMALIZERS': str(inbox),
                'CONVERSION_NORMALIZER_LIMIT': str(budget),
            },
            'conversion-collector.env': base('collector'),
            'conversion-callback.env': {
                **base('callback'), **mock,
                'CONVERSION_CALLBACK_ADDR': callback_addr,
                'CONVERSION_CREDENTIAL_KEYS': keys_json,
            },
            'conversion-partner-worker.env': {
                **base('partner_worker'), **mock,
                'CONVERSION_PARTNER_REPORT_ENABLED': str(report_enabled).lower(),
                'CONVERSION_INSTANCE_ID': '',
                'CONVERSION_CREDENTIAL_KEYS': keys_json,
            },
            'conversion-ops.env': {**base('ops'), 'CONVERSION_CREDENTIAL_KEYS': keys_json},
        }
        cms_forwarding = {
            'CONVERSION_ENVIRONMENT': environment,
            'CONVERSION_PRIVATE_URL': private_url,
            'CONVERSION_PRIVATE_TOKEN': private_token,
        }
        deploy = {
            'CONVERSION_WORKER_REPLICAS': str(workers),
            'CONVERSION_API_PORT': str(private_port),
            'CONVERSION_API_HOST_PORT': str(host_port),
        }
        if environment == 'test':
            for index in range(1, workers + 1):
                files[f'conversion-worker-{index}.env'] = {
                    **files['conversion-worker.env'],
                    'CONVERSION_INSTANCE_ID': f'conversion-test-worker-{index}',
                }
        return files, cms_forwarding, deploy

    if get('conversion.environment') == 'test':
        if not args.check and (args.output_dir is None or output.resolve() == root):
            sys.exit('测试配置需要独立的 --output-dir，不能覆盖部署配置。')
        files, forwarding, compose = conversion_files(str(get('common.database_url')).strip())
        return generate({
            **files,
            'cms-conversion.env': forwarding,
            'reader-conversion.env': {'CONVERSION_ENVIRONMENT': forwarding['CONVERSION_ENVIRONMENT']},
            '.env': compose,
        })

    tag = str(get('deployment.image_tag', 'local')).strip()
    if not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}', tag):
        problems.append('deployment.image_tag：无效的镜像标签')

    gin_mode = str(get('common.gin_mode')).strip()
    if gin_mode != 'release':
        problems.append('common.gin_mode：线上部署必须为 release')

    tz = str(get('common.tz', 'UTC')).strip()
    if not tz:
        problems.append('common.tz：不能为空')

    dsn = str(get('common.database_url')).strip()
    try:
        db = urlsplit(dsn)
        if (
            db.scheme not in ('postgres', 'postgresql')
            or not db.hostname
            or db.hostname in ('localhost', '127.0.0.1', 'postgres')
            or not (1 <= (db.port or 5432) <= 65535)
        ):
            raise ValueError()
        user = unquote(db.username or '')
        password = unquote(db.password or '')
        database = unquote(db.path.lstrip('/'))
        if not user or not password or not database:
            raise ValueError()
        if password in ('password', 'postgres', 'meta') or is_placeholder(dsn):
            problems.append('common.database_url：请使用有效的线上 RDS 连接串及密码')
    except Exception:
        problems.append('common.database_url：需要外部 RDS 的 postgres://用户:URL编码密码@主机/数据库?sslmode=disable 格式')

    redis_url = str(get('common.redis_url')).strip()
    if redis_url != 'redis:6379':
        problems.append('common.redis_url：本 Compose 使用内部 redis:6379')

    jwt_secret = str(get('common.jwt_secret')).strip()
    if not jwt_secret or is_placeholder(jwt_secret) or jwt_secret in DEFAULT_DEV_JWT_SECRETS or len(jwt_secret) < 16:
        problems.append('common.jwt_secret：生产模式必须设置至少 16 字符的非默认强密钥')
    cms_jwt_secret = str(get('cms.jwt_secret', '')).strip()
    if len(cms_jwt_secret) < 32 or cms_jwt_secret == jwt_secret or is_placeholder(cms_jwt_secret):
        problems.append('cms.jwt_secret：必须至少 32 字符，且与 Reader 密钥不同')


    storage_type = str(get('storage.type', 'postgres')).strip().lower()
    if storage_type not in ('postgres', 'oss'):
        problems.append('storage.type：仅支持 postgres 或 oss')
    oss_endpoint = str(get('storage.oss_endpoint', '')).strip()
    oss_ak = str(get('storage.oss_access_key_id', '')).strip()
    oss_sk = str(get('storage.oss_access_key_secret', '')).strip()
    oss_bucket = str(get('storage.oss_bucket', '')).strip()
    oss_base_path = str(get('storage.oss_base_path', 'novels')).strip() or 'novels'
    if storage_type == 'oss':
        for field_name, field_val in [
            ('storage.oss_endpoint', oss_endpoint),
            ('storage.oss_access_key_id', oss_ak),
            ('storage.oss_access_key_secret', oss_sk),
            ('storage.oss_bucket', oss_bucket),
        ]:
            if not field_val or is_placeholder(field_val):
                problems.append(f'{field_name}：启用 OSS 章节存储时必须填写有效值')

    cover_enabled_raw = get('cover_storage.oss.enabled', False)
    if not isinstance(cover_enabled_raw, bool):
        problems.append('cover_storage.oss.enabled：必须为布尔值 true 或 false')
        cover_enabled = False
    else:
        cover_enabled = cover_enabled_raw

    cover_public_read_raw = get('cover_storage.oss.public_read', False)
    if not isinstance(cover_public_read_raw, bool):
        problems.append('cover_storage.oss.public_read：必须为布尔值 true 或 false')
        cover_public_read = False
    else:
        cover_public_read = cover_public_read_raw

    cover_region = str(get('cover_storage.oss.region', 'us-west-1')).strip()
    cover_endpoint = str(get('cover_storage.oss.endpoint', '')).strip()
    cover_bucket = str(get('cover_storage.oss.bucket', '')).strip()
    cover_prefix = str(get('cover_storage.oss.prefix', 'covers')).strip().strip('/') or 'covers'
    cover_public_url = str(get('cover_storage.oss.public_base_url', '')).strip()
    cover_ttl = str(get('cover_storage.oss.signed_url_ttl_seconds', '3600')).strip()
    cover_max_bytes = str(get('cover_storage.oss.max_upload_bytes', '5242880')).strip()

    runtime_oss = {
        'ALIBABA_CLOUD_ACCESS_KEY_ID': str(get('deployment.oss_credentials.access_key_id', '')).strip(),
        'ALIBABA_CLOUD_ACCESS_KEY_SECRET': str(get('deployment.oss_credentials.access_key_secret', '')).strip(),
        'ALIBABA_CLOUD_SECURITY_TOKEN': str(get('deployment.oss_credentials.security_token', '')).strip(),
    }

    if cover_enabled:
        for field_name, field_val in [
            ('cover_storage.oss.region', cover_region),
            ('cover_storage.oss.endpoint', cover_endpoint),
            ('cover_storage.oss.bucket', cover_bucket),
            ('cover_storage.oss.prefix', cover_prefix),
            ('cover_storage.oss.public_base_url', cover_public_url),
        ]:
            if not field_val or is_placeholder(field_val):
                problems.append(f'{field_name}：启用封面 OSS 时必须填写有效值')
        parsed_pub = urlsplit(cover_public_url)
        if parsed_pub.scheme not in ('http', 'https') or not parsed_pub.netloc:
            problems.append('cover_storage.oss.public_base_url：必须为有效的 http:// 或 https:// 自定义域名地址')
        if not cover_public_read and (not cover_ttl.isdigit() or int(cover_ttl) <= 0):
            problems.append('cover_storage.oss.signed_url_ttl_seconds：必须为正整数秒数')
        elif cover_public_read and not cover_ttl:
            cover_ttl = '3600'
        if not cover_max_bytes.isdigit() or int(cover_max_bytes) <= 0:
            problems.append('cover_storage.oss.max_upload_bytes：必须为正整数字节数')
        for cred_key in ('ALIBABA_CLOUD_ACCESS_KEY_ID', 'ALIBABA_CLOUD_ACCESS_KEY_SECRET'):
            if not runtime_oss[cred_key] or is_placeholder(runtime_oss[cred_key]):
                problems.append(f'deployment.oss_credentials ({cred_key})：启用封面 OSS 时必须填写有效凭证')

    payment_common, payment_cms, payment_reader, payment_account_env = payment_files()
    fb_test_code = str(get('tracking.fb_test_event_code', '')).strip()
    meta_api_ver = str(get('tracking.meta_api_version', '')).strip()

    cms_port = str(get('cms.port', '')).strip()
    reader_port = str(get('reader.port', '')).strip()
    for field_name, port_val in [('cms.port', cms_port), ('reader.port', reader_port)]:
        if not port_val.isdigit() or not (1 <= int(port_val) <= 65535):
            problems.append(f'{field_name}：必须为 1–65535 的端口号')
    if cms_port.isdigit() and reader_port.isdigit() and cms_port == reader_port:
        problems.append('cms.port 与 reader.port 不能相同')

    cms_domain = str(get('cms.default_domain', '')).strip()
    reader_domain = str(get('reader.default_domain', '')).strip()
    for field_name, domain_val in [('cms.default_domain', cms_domain), ('reader.default_domain', reader_domain)]:
        parsed = urlsplit(domain_val)
        if parsed.scheme not in ('http', 'https') or not parsed.netloc or is_placeholder(domain_val):
            problems.append(f'{field_name}：必须为有效的 http:// 或 https:// 域名地址')

    conversion_envs, conversion_cms, conversion_compose = conversion_files(dsn)

    compose_env = {
        **conversion_compose,
        'RELEASE_VERSION': tag,
        'APP_TZ': tz,
        'CMS_PORT': cms_port,
        'READER_PORT': reader_port,
    }

    shared_env = {
        'GIN_MODE': gin_mode,
        'DATABASE_URL': dsn,
        'REDIS_URL': redis_url,
        'JWT_SECRET': jwt_secret,
        'STORAGE_TYPE': storage_type,
        **payment_common,
        'OSS_ENDPOINT': oss_endpoint,
        'OSS_ACCESS_KEY_ID': oss_ak,
        'OSS_ACCESS_KEY_SECRET': oss_sk,
        'OSS_BUCKET': oss_bucket,
        'OSS_BASE_PATH': oss_base_path,
        'COVER_OSS_ENABLED': 'true' if cover_enabled else 'false',
        'COVER_OSS_PUBLIC_READ': 'true' if cover_public_read else 'false',
        'COVER_OSS_REGION': cover_region,
        'COVER_OSS_BUCKET': cover_bucket,
        'COVER_OSS_PREFIX': cover_prefix,
        'COVER_OSS_PUBLIC_BASE_URL': cover_public_url,
        'COVER_OSS_SIGNED_URL_TTL_SECONDS': cover_ttl,
    }
    if fb_test_code:
        shared_env['FB_TEST_EVENT_CODE'] = fb_test_code
    if meta_api_ver:
        shared_env['META_API_VERSION'] = meta_api_ver

    cms_env = {
        'PORT': cms_port,
        'CMS_JWT_SECRET': cms_jwt_secret,
        'DEFAULT_DOMAIN': cms_domain,
        'COVER_OSS_ENDPOINT': cover_endpoint,
        'COVER_MAX_UPLOAD_BYTES': cover_max_bytes,
        **conversion_cms,
        **shared_env,
        **payment_cms,
    }

    reader_env = {
        'PORT': reader_port,
        'DEFAULT_DOMAIN': reader_domain,
        'CONVERSION_ENVIRONMENT': conversion_cms['CONVERSION_ENVIRONMENT'],
        **shared_env,
        **payment_reader,
    }
    if payment_account_env.keys() & (reader_env.keys() | runtime_oss.keys()):
        problems.append('payment.account_environment：不能覆盖已有的 Reader 环境变量')
    else:
        reader_env.update(payment_account_env)

    return generate({
        **conversion_envs,
        '.env': compose_env,
        'cms.env': cms_env,
        'reader.env': reader_env,
        'runtime.env': runtime_oss,
    })

if __name__ == '__main__':
    sys.exit(main())
