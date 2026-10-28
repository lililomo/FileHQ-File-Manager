#!/www/server/panel/pyenv/bin/python3
# coding: utf-8
"""
Optional automation of the aaPanel steps for file.kppbwi.app (SPEC §12.3,
docs/DEPLOY-AAPANEL.md). The UI remains the primary path; this mirrors
proxy.kppbwi.app/deploy/aapanel/site.py and calls the panel's internal API
(mod/project/proxy/comMod.py, read on aaPanel 8.0.7) so the site is recorded in
the panel database and listed under Website -> Reverse Proxy.
Perlu verifikasi on any other aaPanel version. Safe to re-run.

    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py status
    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py create
    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py configure   # timeouts + nginx-aapanel.conf parts A/B
    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py ssl
    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py force-https

Options (Step 23e — the optional second site for the separate user-content
origin, docs/DEPLOY-AAPANEL.md §13; defaults reproduce the main site exactly):

    --domain  usercontent.file.kppbwi.app     (default file.kppbwi.app)
    --snippet deploy/nginx-usercontent.conf   (default deploy/nginx-aapanel.conf)
    --remark  "FileManager content"           (default FileManager)
    --target  http://127.0.0.1:8080           (default; same binary for both sites)

    /www/server/panel/pyenv/bin/python3 deploy/aapanel/site.py --domain usercontent.file.kppbwi.app --snippet deploy/nginx-usercontent.conf --remark "FileManager content" create
    …                                                           --domain usercontent.file.kppbwi.app --snippet deploy/nginx-usercontent.conf configure
    …                                                           --domain usercontent.file.kppbwi.app ssl
    …                                                           --domain usercontent.file.kppbwi.app force-https

Never prints certificates or keys.
"""
import json
import os
import sys

PANEL = '/www/server/panel'
DOMAIN = 'file.kppbwi.app'
TARGET = 'http://127.0.0.1:8080'
REMARK = 'FileManager'
SNIPPET = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'nginx-aapanel.conf')
TIMEOUTS = {'proxy_connect_timeout': '10', 'proxy_send_timeout': '3600', 'proxy_read_timeout': '3600'}


def parse_args(argv):
    """`[--domain D] [--snippet F] [--remark R] [--target T] <command>`; the
    command may come first or last. Returns (command, options)."""
    opts = {'domain': DOMAIN, 'snippet': SNIPPET, 'remark': REMARK, 'target': TARGET}
    command = ''
    i = 0
    while i < len(argv):
        a = argv[i]
        if a.startswith('--') and a[2:] in opts:
            if i + 1 >= len(argv):
                raise SystemExit('%s needs a value' % a)
            opts[a[2:]] = argv[i + 1]
            i += 2
            continue
        if a.startswith('--'):
            raise SystemExit('unknown option %s' % a)
        if command:
            raise SystemExit('one command only (got %s and %s)' % (command, a))
        command = a
        i += 1
    return command, opts


if __name__ == '__main__':
    _command, _opts = parse_args(sys.argv[1:])
    DOMAIN, REMARK, TARGET = _opts['domain'], _opts['remark'], _opts['target']
    SNIPPET = _opts['snippet'] if os.path.isabs(_opts['snippet']) else os.path.join(os.getcwd(), _opts['snippet'])

snippet_path = os.path.abspath(SNIPPET)
os.chdir(PANEL)
for p in (PANEL, PANEL + '/class', PANEL + '/class_v2'):
    if p not in sys.path:
        sys.path.insert(0, p)

import public  # noqa: E402
from mod.project.proxy.comMod import main as ProxyProject  # noqa: E402


def site_row():
    row = public.M('sites').where('name=?', (DOMAIN,)).find()
    return row if isinstance(row, dict) and row.get('id') else None


def ok(result):
    """The panel answers {'status': True/False, 'msg'} or {'status': 0/-1, 'message'}."""
    if not isinstance(result, dict):
        return False
    status = result.get('status')
    return status is True or (status == 0 and status is not False)


def message(result):
    if not isinstance(result, dict):
        return str(result)[:300]
    msg = result.get('msg', result.get('message', ''))
    if isinstance(msg, dict):
        msg = msg.get('result', msg.get('msg', json.dumps(msg)[:300]))
    return str(msg)[:400]


def snippet_part(name):
    begin, end = '# --- BEGIN PART %s' % name, '# --- END PART %s' % name
    out, on = [], False
    with open(snippet_path, encoding='utf-8') as fh:
        for line in fh:
            if line.startswith(begin):
                on = True
                continue
            if line.startswith(end):
                break
            if on:
                out.append(line.rstrip('\n'))
    if not out:
        raise SystemExit('part %s not found in %s' % (name, snippet_path))
    return '\n'.join(out)


def cmd_status():
    row = site_row()
    if not row:
        print('site: NOT in aaPanel yet')
        return 1
    conf = '{}/vhost/nginx/{}.conf'.format(PANEL, DOMAIN)
    text = public.readFile(conf) or ''
    domains = [d['name'] for d in public.M('domain').where('pid=?', (row['id'],)).select()]
    print('site: id={} name={} project_type={} status={} path={}'.format(
        row['id'], row['name'], row['project_type'], row['status'], row['path']))
    print('domain: {}'.format(', '.join(domains)))
    print('vhost: {} ({})'.format(conf, 'present' if text else 'MISSING'))
    print('proxy_pass to the app: {}'.format('yes' if TARGET in text else 'NO'))
    print('part A (proxy_request_buffering off): {}'.format('yes' if 'proxy_request_buffering off' in text else 'NO'))
    part_b_marker = 'location = /readyz' if 'nginx-aapanel.conf' in snippet_path else 'real_ip_header CF-Connecting-IP'
    print('part B ({}): {}'.format(part_b_marker, 'yes' if part_b_marker in text else 'NO'))
    print('read timeout 3600s: {}'.format('yes' if 'proxy_read_timeout 3600s' in text else 'NO'))
    print('ssl: {}'.format('on' if 'ssl_certificate ' in text else 'off'))
    print('force https: {}'.format('yes' if 'HTTP_TO_HTTPS_START' in text else 'no'))
    return 0


def cmd_create():
    if site_row():
        print('site exists — not created again')
        return cmd_status()
    get = public.to_dict_obj({
        'domains': DOMAIN,
        'proxy_pass': TARGET,
        'proxy_host': '$http_host',
        'proxy_path': '/',
        'proxy_type': 'http',
        'keepuri': 1,
        'remark': REMARK,
    })
    result = ProxyProject().create(get)
    print('create: {} — {}'.format('OK' if ok(result) else 'FAILED', message(result)))
    return 0 if ok(result) and site_row() else 1


def cmd_configure():
    if not site_row():
        print('site missing — run create first')
        return 1
    project = ProxyProject()
    args = {'site_name': DOMAIN, 'proxy_path': '/', 'proxy_pass': TARGET, 'proxy_host': '$http_host',
            'proxy_type': 'http', 'websocket': 1, 'keepuri': 1, 'remark': REMARK}
    args.update(TIMEOUTS)
    steps = [
        ('timeouts 10/3600/3600 s', lambda: project.set_url_proxy(public.to_dict_obj(args))),
        ('part A → custom configuration of "/"', lambda: project.set_url_custom_conf(public.to_dict_obj(
            {'site_name': DOMAIN, 'proxy_path': '/', 'custom_conf': snippet_part('A')}))),
        ('part B → server block', lambda: project.save_config(public.to_dict_obj(
            {'site_name': DOMAIN, 'conf_type': 'server_block', 'body': snippet_part('B')}))),
    ]
    for name, step in steps:
        result = step()
        print('{}: {} — {}'.format(name, 'OK' if ok(result) else 'FAILED', message(result)))
        if not ok(result):
            return 1
    return cmd_status()


def cmd_ssl():
    row = site_row()
    if not row:
        print('site missing')
        return 1
    project = ProxyProject()
    get = public.to_dict_obj({
        'site_name': DOMAIN,
        'domains': json.dumps([DOMAIN]),
        'auth_type': 'http',
        'auth_to': str(row['id']),
        'auto_wildcard': '0',
        'id': str(row['id']),
    })
    cert = project.apply_cert_api(get)
    if not ok(cert):
        print('certificate request: FAILED — {}'.format(message(cert)))
        return 1
    key = cert.get('private_key')
    chain = (cert.get('cert') or '') + (cert.get('root') or '')
    if not key or 'BEGIN CERTIFICATE' not in chain:
        print('certificate request: unexpected answer (fields: {})'.format(', '.join(sorted(cert.keys()))))
        return 1
    print('certificate request: OK')
    result = project.set_ssl(public.to_dict_obj({'site_name': DOMAIN, 'key': key, 'csr': chain}))
    print('certificate install: {} — {}'.format('OK' if ok(result) else 'FAILED', message(result)))
    return 0 if ok(result) else 1


def cmd_force_https():
    if not site_row():
        print('site missing')
        return 1
    result = ProxyProject().set_force_https(public.to_dict_obj({'site_name': DOMAIN, 'force_https': 1}))
    print('force https: {} — {}'.format('OK' if ok(result) else 'FAILED', message(result)))
    return 0 if ok(result) else 1


COMMANDS = {'status': cmd_status, 'create': cmd_create, 'configure': cmd_configure,
            'ssl': cmd_ssl, 'force-https': cmd_force_https}

if __name__ == '__main__':
    if _command not in COMMANDS:
        print(__doc__)
        sys.exit(2)
    if not os.path.isfile(snippet_path):
        raise SystemExit('snippet not found: %s' % snippet_path)
    sys.exit(COMMANDS[_command]())
