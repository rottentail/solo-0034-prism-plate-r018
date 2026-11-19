#!/usr/bin/env python3
"""Run bounded production HTTP journeys against an isolated ephemeral service."""
import argparse, concurrent.futures, copy, hashlib, json, os, pathlib, signal, socket, struct, subprocess, tempfile, time, urllib.error, urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[1]
def main():
    parser=argparse.ArgumentParser();parser.add_argument('--workflow',required=True);args=parser.parse_args()
    seed=json.loads((ROOT/'examples/amber-proof.json').read_text())
    with tempfile.TemporaryDirectory(prefix='prism-plate-') as tmp:
        base=pathlib.Path(tmp);binary=base/'prismplate';subprocess.run(['go','build','-o',str(binary),'./cmd/prismplate'],cwd=ROOT,check=True)
        sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close();url=f'http://127.0.0.1:{port}'
        log=(base/'service.log').open('w');proc=None
        def start():
            nonlocal proc
            proc=subprocess.Popen([str(binary),'-listen',f'127.0.0.1:{port}','-data',str(base/'state')],stdout=log,stderr=log)
            for _ in range(100):
                if proc.poll() is not None:raise RuntimeError('service exited')
                try:
                    urllib.request.urlopen(url+'/healthz',timeout=1).close();return
                except OSError:time.sleep(.05)
            raise RuntimeError('startup timed out')
        def stop():
            if proc and proc.poll() is None:
                proc.send_signal(signal.SIGTERM)
                try:proc.wait(timeout=45)
                except subprocess.TimeoutExpired:proc.kill();proc.wait();raise
        def call(method,path,data=None,status=200,raw=False,headers=None):
            body=data if isinstance(data,bytes) else (json.dumps(data).encode() if data is not None else None)
            req=urllib.request.Request(url+path,data=body,method=method,headers=headers or {'Content-Type':'application/json'})
            try:
                with urllib.request.urlopen(req,timeout=35) as r:code=r.status;payload=r.read();hs={k.lower():v for k,v in r.headers.items()}
            except urllib.error.HTTPError as e:code=e.code;payload=e.read();hs={k.lower():v for k,v in e.headers.items()}
            if code!=status:raise RuntimeError(f'{method} {path}: expected {status}, observed {code}: {payload[:200]!r}')
            return (payload,hs) if raw else json.loads(payload) if payload else None
        def render(fmt='png',revision=1):return call('POST',f'/v1/plates/amber-plate/render?revision={revision}&format={fmt}',status=201)
        try:
            start();created=call('POST','/v1/plates',seed,status=201);assert created['revisions'][0]['number']==1
            w=args.workflow
            if w=='create':
                assert call('GET','/v1/plates/amber-plate')['title']==seed['title'];call('POST','/v1/plates',seed,status=409)
                bad=copy.deepcopy(seed);bad['id']='bad-plate';bad['document']['layers'][0]['shapes'][0]['transform']=[0]*6;call('POST','/v1/plates',bad,status=400)
            elif w=='revise':
                q={'expected_revision':1,'document':copy.deepcopy(seed['document'])};q['document']['background']['r']=.2
                with concurrent.futures.ThreadPoolExecutor(2) as pool:
                    def attempt(_):
                        try:return call('PUT','/v1/plates/amber-plate',q)
                        except RuntimeError as e:
                            if 'observed 409' in str(e):return 'conflict'
                            raise
                    results=list(pool.map(attempt,range(2)))
                assert sum(x=='conflict' for x in results)==1;assert len(call('GET','/v1/plates/amber-plate')['revisions'])==2
                assert call('GET','/v1/plates/amber-plate')['revisions'][0]['document']==created['revisions'][0]['document']
            elif w=='branch':
                b=call('POST','/v1/plates/amber-plate/branch',{'revision':1,'id':'amber-fork','title':'琥珀新稿'},status=201);assert b['revisions'][0]['digest']==created['revisions'][0]['digest'];call('POST','/v1/plates/amber-plate/branch',{'revision':99,'id':'missing-fork','title':'无效稿'},status=404)
            elif w=='archive':
                call('POST','/v1/plates/amber-plate/archive');call('PUT','/v1/plates/amber-plate',{'expected_revision':1,'document':seed['document']},status=409);assert render()['revision']==1
            elif w in ('png','svg','artifact'):
                fmt='svg' if w=='svg' else 'png';a=render(fmt);raw,hs=call('GET','/v1/artifacts/'+a['id'],raw=True);assert hashlib.sha256(raw).hexdigest()==a['digest']
                if fmt=='png':assert raw[:8]==b'\x89PNG\r\n\x1a\n';assert struct.unpack('>II',raw[16:24])==(192,144)
                else:assert b'<svg' in raw and b'<linearGradient' in raw and b'clipPath' in raw
                assert render(fmt)['digest']==a['digest'];call('GET','/v1/artifacts/'+a['id'],status=304,raw=True,headers={'If-None-Match':hs['etag']});call('GET','/v1/artifacts/missing',status=404)
                if w=='artifact':call('DELETE','/v1/artifacts/'+a['id']);call('GET','/v1/artifacts/'+a['id'],status=409)
            elif w in ('exposure','cancel'):
                job=call('POST','/v1/plates/amber-plate/exposures?revision=1&format=png',status=202)
                if w=='cancel':call('DELETE','/v1/exposures/'+job['id'],status=202)
                for _ in range(200):
                    job=call('GET','/v1/exposures/'+job['id'])
                    if job['state'] in ('succeeded','cancelled','failed'):break
                    time.sleep(.02)
                assert job['state'] in (('succeeded','cancelled') if w=='cancel' else ('succeeded',))
                if job['state']=='succeeded':assert call('GET','/v1/artifacts/'+job['artifact'],raw=True)[0][:8]==b'\x89PNG\r\n\x1a\n'
                call('DELETE','/v1/exposures/absent',status=404)
            elif w=='proof':
                a=render();call('POST','/v1/proofs',{'id':'proof-amber','artifact':a['id']},status=201);assert call('PUT','/v1/proofs/proof-amber',{'state':'accepted','note':'叠色与镂空检查通过'})['state']=='accepted';call('DELETE','/v1/artifacts/'+a['id'],status=409);call('PUT','/v1/proofs/proof-amber',{'state':'rejected','note':'重复裁决'},status=409)
            elif w in ('bundle','restore'):
                a=render();sealed,_=call('GET','/v1/plates/amber-plate/bundle',raw=True);payload=json.loads(sealed);assert payload['plate']['revisions'][0]['digest']==a['scene_digest']
                if w=='bundle':
                    call('POST','/v1/bundles',sealed,status=409);payload['digest']='0'*64;call('POST','/v1/bundles',payload,status=400)
                    stop();os.rename(base/'state',base/'original');start();imported=call('POST','/v1/bundles',sealed,status=201);assert imported['revisions'][0]['digest']==a['scene_digest'];assert render()['digest']==a['digest']
                else:
                    stop();start();assert call('GET','/v1/artifacts/'+a['id'],raw=True)[0][:8]==b'\x89PNG\r\n\x1a\n';assert render()['digest']==a['digest']
            elif w=='isolation':
                lockproc=subprocess.run([str(binary),'-listen','127.0.0.1:0','-data',str(base/'state')],capture_output=True,timeout=3);assert lockproc.returncode!=0;assert b'locked' in lockproc.stderr
                call('POST','/v1/plates',b'{"id":"x"} true',status=400)
            elif w=='rejection':
                bad=copy.deepcopy(seed);bad['id']='oversize';bad['document']['width']=513;call('POST','/v1/plates',bad,status=400);call('GET','/v1/plates/oversize',status=404)
                bad=copy.deepcopy(seed);bad['id']='invalid-brush';bad['document']['layers'][0]['shapes'][0]['brush']['stops'][1]['at']=0;call('POST','/v1/plates',bad,status=400);assert render()['revision']==1
            elif w=='book':
                bookseed=json.loads((ROOT/'examples/review-book.json').read_text())
                b=call('POST','/v1/books',bookseed,status=201);assert b['id']=='review-book-a' and len(b['revisions'])==1
                assert b['revisions'][0]['pages'][0]['placements'][0]['digest']==created['revisions'][0]['digest']
                call('POST','/v1/books',bookseed,status=409)
                # every page renders with a stable digest; page artifacts record exact pins
                r1=call('POST','/v1/books/review-book-a/render?revision=1',status=201);assert len(r1['pages'])==4
                d1=[p['digest'] for p in r1['pages']];assert len(set(d1))==4
                raw,hs=call('GET','/v1/pages/'+r1['pages'][0]['id'],raw=True);assert raw[:8]==b'\x89PNG\r\n\x1a\n' and hashlib.sha256(raw).hexdigest()==r1['pages'][0]['digest']
                # reported bounds equal the declared unrotated box and nothing overflows
                b0=r1['pages'][0]['report']['placements'][0]
                assert (b0['bounds']['x'],b0['bounds']['y'],b0['bounds']['w'],b0['bounds']['h'])==(16,16,192,144)
                assert b0['clipped_area']==0 and b0['role']=='single' and b0['span']==1
                assert (b0['content']['x'],b0['content']['y'],b0['content']['w'],b0['content']['h'])==(16,16,192,144)
                # a 90-degree rotation keeps the box, swaps its reported extent about the same center
                b1=r1['pages'][1]['report']['placements'][0]
                assert abs((b1['bounds']['x']+b1['bounds']['w']/2)-(20+160/2))<1e-9
                assert abs((b1['bounds']['y']+b1['bounds']['h']/2)-(40+120/2))<1e-9
                assert abs(b1['bounds']['w']-120)<1e-9 and abs(b1['bounds']['h']-160)<1e-9 and b1['clipped_area']==0
                assert struct.unpack('>II',raw[16:24])==(240,180)
                again=call('POST','/v1/books/review-book-a/render?revision=1&page=1',status=200);assert again['page']['id']==r1['pages'][0]['id'] and again['created'] is False
                # reorder moves pages with their pinned sources, never rewires them
                rev=call('POST','/v1/books/review-book-a/reorder',{'expected_revision':1,'order':[2,1,3,4]},status=201)
                p0=rev['revisions'][1]['pages'][0];assert p0['placements'][0]['revision']==1 and p0['placements'][0]['rotation']==90
                r2=call('POST','/v1/books/review-book-a/render?revision=2',status=201)
                assert r2['pages'][0]['digest']==d1[1] and r2['pages'][1]['digest']==d1[0]
                assert r2['pages'][2]['digest']==d1[2] and r2['pages'][3]['digest']==d1[3]
                assert r2['pages'][0]['id']!=r1['pages'][0]['id']
                # the swapped page's recorded pins still name the source that painted it
                assert r2['pages'][0]['sources'][0]['plate']=='amber-plate' and r2['pages'][0]['sources'][0]['revision']==1
                assert r2['pages'][0]['report']['placements'][0]['pin']==r2['pages'][0]['sources'][0]
                # stale revision, out-of-range page, duplicate reorder and missing pin are refused
                call('PUT','/v1/books/review-book-a',{'expected_revision':1,'pages':bookseed['pages']},status=409)
                call('POST','/v1/books/review-book-a/reorder',{'expected_revision':2,'order':[1,1,3,4]},status=400)
                call('POST','/v1/books/review-book-a/render?revision=2&page=9',status=404)
                bad=copy.deepcopy(bookseed);bad['id']='book-missing';bad['pages'][0]['placements'][0]['revision']=99
                call('POST','/v1/books',bad,status=404)
                bad=copy.deepcopy(bookseed);bad['id']='book-baddigest';bad['pages'][0]['placements'][0]['digest']='0'*64
                call('POST','/v1/books',bad,status=409)
                # overflow and safe-area outcomes remain explainable per placement
                over=copy.deepcopy(bookseed);over['id']='book-overflow'
                over['pages'][0]['placements'][0].update({'x':-30,'y':-20,'rotation':15})
                call('POST','/v1/books',over,status=201)
                orr=call('POST','/v1/books/book-overflow/render?revision=1&page=1',status=201)['page']
                rep=orr['report']['placements'][0]
                assert rep['clipped_area']>0 and rep['overflow']['left']>0 and rep['overflow']['top']>0 and rep['exceeds_safe_area'] is True
                assert orr['report']['safe_violated'] is True
                # cross-page placement: one transform sliced across the gutter
                spread=copy.deepcopy(bookseed);spread['id']='book-spread'
                spread['pages'][0]['placements']=[{'plate':'amber-plate','revision':1,'x':40,'y':30,'width':200,'height':120,'rotation':0,'fit':'stretch','span':2}]
                spread['pages'][1]['placements']=[]
                sb=call('POST','/v1/books',spread,status=201)
                sr=call('POST','/v1/books/book-spread/render?revision=1',status=201)['pages']
                assert sr[0]['report']['placements'][0]['role']=='anchor' and sr[0]['report']['placements'][0]['span']==2
                assert sr[1]['report']['placements'][0]['role']=='continuation' and sr[1]['report']['placements'][0]['anchor_page']==1
                assert sr[0]['sources']==sr[1]['sources'] and len(sr[0]['sources'])==1
                assert sr[0]['digest']!=sr[1]['digest']
                # slice origins differ by exactly one page width: one continuous spread transform
                c0=sr[0]['report']['placements'][0]['content'];c1=sr[1]['report']['placements'][0]['content']
                assert abs((c0['x']-240)-c1['x'])<1e-9 and abs(c0['y']-c1['y'])<1e-9 and abs(c0['w']-c1['w'])<1e-9
                # the two slices are the two halves of one continuous placement
                zsf=call('GET','/v1/books/book-spread/handover?revision=1',raw=True)[0]
                import io,zipfile
                zm=json.loads(zipfile.ZipFile(io.BytesIO(zsf)).read('manifest.json'))
                assert [p['placements'][0]['role'] for p in zm['pages'][:2]]==['anchor','continuation']
                # a spread cannot be split by reorder; moving it whole is allowed
                call('POST','/v1/books/book-spread/reorder',{'expected_revision':1,'order':[1,3,2,4]},status=400)
                moved=call('POST','/v1/books/book-spread/reorder',{'expected_revision':1,'order':[3,4,1,2]},status=201)
                mr=call('POST','/v1/books/book-spread/render?revision=2',status=201)['pages']
                assert mr[2]['report']['placements'][0]['role']=='anchor' and mr[3]['report']['placements'][0]['role']=='continuation'
                # a span running past the last page, or onto a different trim, is refused
                bad=copy.deepcopy(spread);bad['id']='book-span-end';bad['pages'][3]['placements']=[{'plate':'amber-plate','revision':1,'x':0,'y':0,'width':200,'height':120,'rotation':0,'fit':'contain','span':2}]
                call('POST','/v1/books',bad,status=400)
                bad=copy.deepcopy(spread);bad['id']='book-span-size';bad['pages'][1]['width']=200
                call('POST','/v1/books',bad,status=400)
                # a placement whose box leaves the page entirely still renders an explainable result
                off=copy.deepcopy(bookseed);off['id']='book-offpage'
                off['pages'][0]['placements'][0].update({'x':-400,'y':-400,'width':100,'height':100})
                call('POST','/v1/books',off,status=201)
                opr=call('POST','/v1/books/book-offpage/render?revision=1&page=1',status=201)['page']['report']['placements'][0]
                assert opr['off_page'] is True and opr['clipped_area']==10000 and opr['overflow']['left']==400 and opr['overflow']['top']==400 and opr['overflow']['right']==0
                # rotation and safe-area violations co-exist without moving the placement
                tilt=copy.deepcopy(bookseed);tilt['id']='book-tilt'
                tilt['pages'][0]['placements'][0].update({'rotation':45})
                call('POST','/v1/books',tilt,status=201)
                tr=call('POST','/v1/books/book-tilt/render?revision=1&page=1',status=201)['page']
                assert tr['report']['safe_violated'] is True and tr['report']['placements'][0]['exceeds_safe_area'] is True
                assert abs(tr['report']['placements'][0]['bounds']['x']-tr['report']['placements'][0]['content']['x'])>0
                # rendering a revoked page artifact stays refused
                call('DELETE','/v1/pages/'+r2['pages'][2]['id']);call('GET','/v1/pages/'+r2['pages'][2]['id'],status=409)
                # book facts, page images and pins survive a clean restart
                stop();start()
                assert call('GET','/v1/books/review-book-a')['revisions'][1]['digest']==rev['revisions'][1]['digest']
                assert call('GET','/v1/pages/'+r1['pages'][0]['id'],raw=True)[0][:8]==b'\x89PNG\r\n\x1a\n'
                assert call('POST','/v1/books/review-book-a/render?revision=2&page=1',status=200)['created'] is False
                # offline handover: deterministic zip, manifest maps each page to its pins
                z1,z1h=call('GET','/v1/books/review-book-a/handover?revision=2',raw=True)
                z2,_=call('GET','/v1/books/review-book-a/handover?revision=2',raw=True)
                assert z1==z2 and z1[:2]==b'PK'
                call('GET','/v1/books/review-book-a/handover?revision=2',status=304,raw=True,headers={'If-None-Match':z1h['etag']})
                zf=zipfile.ZipFile(io.BytesIO(z1));names=zf.namelist()
                assert 'manifest.json' in names and 'pages/page-001.png' in names and 'pages/page-004.png' in names
                man=json.loads(zf.read('manifest.json'));assert man['book']['revision']==2 and len(man['pages'])==4
                assert [p['number'] for p in man['pages']]==[1,2,3,4]
                assert man['pages'][0]['placements'][0]['pin']['revision']==1
                assert hashlib.sha256(zf.read('pages/page-001.png')).hexdigest()==man['pages'][0]['digest']
                src=man['sources'][0];assert hashlib.sha256(json.dumps(json.loads(zf.read(src['file'])),separators=(',',':')).encode()).hexdigest()==src['digest']
                # the delivered archive verifies offline, byte for byte, with no data root
                hf=base/'delivery.zip';hf.write_bytes(z1)
                verify=subprocess.run([str(binary),'-verify-handover',str(hf)],capture_output=True,text=True)
                assert verify.returncode==0,verify.stderr
                assert json.loads(verify.stdout)['pages']==4
                badzip=bytearray(z1);badzip[len(badzip)//2]^=0xff;hf.write_bytes(bytes(badzip))
                assert subprocess.run([str(binary),'-verify-handover',str(hf)],capture_output=True).returncode!=0
                # swapping pages that pin different revisions must not re-attach sources
                rv={'expected_revision':1,'document':copy.deepcopy(seed['document'])};rv['document']['background']['b']=.6
                call('PUT','/v1/plates/amber-plate',rv)
                two=copy.deepcopy(bookseed);two['id']='book-tworev';two['pages']=two['pages'][:2]
                two['pages'][0]['placements'][0].update({'revision':1})
                two['pages'][1]['placements'][0].update({'revision':2,'rotation':0,'fit':'contain'})
                call('POST','/v1/books',two,status=201)
                t1=call('POST','/v1/books/book-tworev/render?revision=1',status=201)['pages']
                assert t1[0]['sources'][0]['revision']==1 and t1[1]['sources'][0]['revision']==2
                d_old,d_new=t1[0]['digest'],t1[1]['digest'];assert d_old!=d_new
                call('POST','/v1/books/book-tworev/reorder',{'expected_revision':1,'order':[2,1]},status=201)
                t2=call('POST','/v1/books/book-tworev/render?revision=2',status=201)['pages']
                assert t2[0]['sources'][0]['revision']==2 and t2[0]['digest']==d_new
                assert t2[1]['sources'][0]['revision']==1 and t2[1]['digest']==d_old
                assert t2[0]['report']['placements'][0]['pin']==t2[0]['sources'][0]
                # archive blocks further revisions but keeps rendering history
                call('POST','/v1/books/review-book-a/archive');call('POST','/v1/books/review-book-a/reorder',{'expected_revision':2,'order':[1,2,3,4]},status=409)
                assert call('GET','/v1/pages/'+r1['pages'][0]['id'],raw=True)[0][:8]==b'\x89PNG\r\n\x1a\n'
            else:raise RuntimeError('unknown workflow '+w)
            print(json.dumps({'workflow':w,'result':'passed','entry':'HTTP','port':port},ensure_ascii=False))
        finally:stop();log.close()
if __name__=='__main__':main()
