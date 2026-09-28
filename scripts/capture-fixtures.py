import json, urllib.request, sys, os, time
B = sys.argv[1] if len(sys.argv) > 1 else 'http://localhost:8084'
OUT = sys.argv[2] if len(sys.argv) > 2 else 'fixtures'
def get(p):
    return json.load(urllib.request.urlopen(B + p, timeout=600))
def post(p, body):
    r = urllib.request.Request(B + p, data=json.dumps(body).encode(), headers={'Content-Type': 'application/json'})
    return json.load(urllib.request.urlopen(r, timeout=600))
def save(name, obj):
    with open(os.path.join(OUT, name + '.json'), 'w') as f:
        json.dump(obj, f, indent=1, sort_keys=True); f.write('\n')
    print('saved', name, list(obj.keys()) if isinstance(obj, dict) else type(obj))

def walk(subject, save_as):
    start = post('/exchange', {'subject': subject, 'phase': 'start'})
    if save_as: save('exchange-start', start)
    ch = start['chapter']; scr = ch['check'][0]; assert scr['check'] == 'screener', scr
    placed = post('/exchange', {'subject': subject, 'phase': 'boundary', 'unit': ch['unit'],
                                'check_responses': [{'item_id': scr['id'], 'selected_index': 2, 'confidence': 3}]})
    if save_as: save('exchange-screener', placed)
    series = placed['chapter']; assert series.get('calibration') and len(series['check']) > 1
    resp = []
    for it in series['check']:
        if it['kind'] == 'mcq':
            idx = next(i for i, o in enumerate(it['reveal']['options']) if o['correct'])
            resp.append({'item_id': it['id'], 'selected_index': idx, 'confidence': 4})
        elif it['check'] == 'llm':
            resp.append({'item_id': it['id'], 'idk': True, 'confidence': 1})
        else:
            resp.append({'item_id': it['id'], 'response': it['reveal']['answer'], 'confidence': 4})
    # The iPad always exchanges async: grades now, the chapter from /chapter
    # once the server stops authoring.
    graded = post('/exchange', {'subject': subject, 'phase': 'boundary', 'unit': series['unit'], 'check_responses': resp, 'chunk_minutes': 3.5, 'async': True})
    print(subject, 'gate', graded.get('gate'), 'authoring', graded.get('authoring'), 'extra keys', [k for k in graded if k not in ('results','gate','chapter','state','break_suggestion')])
    if save_as: save('exchange-series', graded)
    for _ in range(600):
        status = get('/chapter/' + subject)
        if not status['authoring']: break
        time.sleep(0.5)
    assert not status['authoring_error'], status['authoring_error']
    chapter = status['chapter']; assert chapter and chapter['unit'] == graded['authoring'], status
    if save_as: save('chapter', status)
    # A teaching chapter failed outright, after a long chunk: a failing gate
    # and a break suggestion in one response, and nothing authoring - after
    # a failed gate the reader chooses (repair, remediate or override) first.
    wrong = []
    for it in chapter['check']:
        if it['kind'] == 'mcq':
            wrong.append({'item_id': it['id'], 'selected_index': 99, 'confidence': 4})
        else:
            wrong.append({'item_id': it['id'], 'response': 'definitely wrong', 'confidence': 4})
    failed = post('/exchange', {'subject': subject, 'phase': 'boundary', 'unit': chapter['unit'], 'check_responses': wrong, 'chunk_minutes': 25, 'async': True})
    print(subject, 'fail gate', failed.get('gate'), 'authoring', failed.get('authoring'), 'break', failed.get('break_suggestion'))
    if save_as: save('exchange-fail', failed)
    for _ in range(600):
        if not get('/chapter/' + subject)['authoring']: break
        time.sleep(0.5)

walk('ai', True)
walk('data', False)
save('subjects', get('/subjects'))
save('state', get('/state?subject=ai'))
for s in ('ai', 'data'):
    post('/reset', {'subject': s, 'confirm': True})
print('reset both; active now', get('/subjects')['active'])
