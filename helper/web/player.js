(function(){
  var video=document.getElementById('video'),end=document.getElementById('end'),buffering=document.getElementById('buffering'),title=document.getElementById('title'),titleText=document.getElementById('title-text'),status=document.getElementById('status'),hint=document.getElementById('hint');
  var copy={
    zh:{connecting:'正在连接 IINA…',defaultTitle:'续播文件',videoLabel:'Continue 播放器',end:'结束续播',track:'当前字幕',preparing:'正在准备，请稍候…',buffering:'正在缓冲，请稍候…',slow:'当前网速较慢，正在继续加载…',seeking:'正在跳转…',syncing:'正在从 IINA 同步…',canStart:'点击播放可续播',playing:'正在此设备播放，在 IINA 中点击播放即可返回',paused:'已在此设备暂停',macPlaying:'正在 IINA 播放，点击播放即可续播',connected:'已连接 IINA',closed:'续播服务已结束',next:'可从 IINA 重新开始',ended:'可从 IINA 重新开始',lost:'与 IINA 的连接已断开',wifi:'请确认两台设备连接的是同一个 Wi-Fi',playFailed:'播放失败，请重试',seekFailed:'跳转失败，请重试',loadFailed:'文件载入失败，请在 IINA 继续播放后重试',processingFailed:'文件处理失败，请在 IINA 结束续播服务后重试',warnings:{image_subtitle:'字幕格式暂不支持，仍可继续播放',subtitle_unreadable:'字幕读取失败，仍可继续播放',subtitle_conversion:'字幕处理失败，仍可继续播放',subtitle_timeline:'字幕时间轴处理失败，仍可继续播放',subtitle_merge:'字幕处理失败，仍可继续播放'}},
    en:{connecting:'Connecting to IINA…',defaultTitle:'Current File',videoLabel:'Continue player',end:'End',track:'Current Subtitles',preparing:'Preparing. Please wait…',buffering:'Buffering. Please wait…',slow:'The network is slow. Still loading…',seeking:'Seeking…',syncing:'Syncing playback information from IINA…',canStart:'Tap Play to continue on this device.',playing:'Playing on this device. Press Play in IINA to continue there.',paused:'Paused on this device.',macPlaying:'Playing in IINA. Tap Play to continue on this device.',connected:'Connected to IINA',closed:'Continue has ended.',next:'Click Start in IINA to begin again.',ended:'Click Start in IINA to begin again.',lost:'Connection to IINA was lost.',wifi:'Make sure both devices are connected to the same Wi-Fi network.',playFailed:'Unable to play right now. Please try again.',seekFailed:'Unable to seek right now. Please try again.',loadFailed:'The file could not be loaded. Continue in IINA, then try again.',processingFailed:'The file could not be processed. Click End in IINA, then try again.',warnings:{image_subtitle:'This subtitle format is not supported, but playback can continue.',subtitle_unreadable:'The subtitles could not be read, but playback can continue.',subtitle_conversion:'The subtitles could not be processed, but playback can continue.',subtitle_timeline:'The subtitle timeline could not be processed, but playback can continue.',subtitle_merge:'The subtitles could not be processed, but playback can continue.'}}
  };
  var language=/^zh\b/i.test(navigator.language||'')?'zh':'en',c=copy[language];
  var state=null,generation=0,revision=0,direct=false,offset=0,started=false,activated=false,connectedSent=false,heartbeat=null,refreshTimer=null,seekTimer=null,pendingSeek=null,seeking=false,retries=0,suppress=false,startingPlayback=false,resumeAfterSeek=false,autoPlay=false,closed=false,pausedAt=0,recovering=false,returningSent=false;
  document.documentElement.lang=language==='zh'?'zh-CN':'en';status.textContent=c.connecting;hint.textContent=c.canStart;end.textContent=c.end;video.setAttribute('aria-label',c.videoLabel);buffering.textContent=c.preparing;
  function setTitle(value){titleText.textContent=value||c.defaultTitle;requestAnimationFrame(updateTitle)}
  function updateTitle(){var distance=Math.max(0,titleText.scrollWidth-title.clientWidth);title.classList.toggle('marquee',distance>4);title.style.setProperty('--title-shift','-'+distance+'px');title.style.setProperty('--title-duration',Math.max(8,distance/24+5)+'s')}
  function absolutePosition(){return Math.max(0,direct?video.currentTime:(offset+(video.currentTime||0)))}
  function send(type,position,paused){return fetch('event',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({type:type,positionSeconds:position==null?absolutePosition():position,paused:paused==null?video.paused:paused})})}
  function pauseQuietly(){suppress=true;video.pause();setTimeout(function(){suppress=false},0)}
  function showBuffering(text){buffering.textContent=text||c.preparing;buffering.hidden=false}
  function warningText(value){if(!value)return'';return value.split(' ').map(function(key){return c.warnings[key]||key}).join(' ')}
  function startHeartbeat(){clearInterval(heartbeat);heartbeat=setInterval(function(){send('heartbeat',null,video.paused)},500)}
  function showClosed(){closed=true;pauseQuietly();clearInterval(heartbeat);clearInterval(refreshTimer);status.textContent=c.closed;hint.textContent=c.next;buffering.hidden=true;end.disabled=true}
  function loadMedia(s){
    direct=s.mode==='direct';offset=direct?0:s.startPositionSeconds;
    var changed=video.dataset.url!==s.mediaUrl||s.generation!==generation;
    if(changed){pauseQuietly();started=false;video.dataset.url=s.mediaUrl;video.src=s.mediaUrl;video.querySelectorAll('track').forEach(function(track){track.remove()});if(s.subtitleUrl){var track=document.createElement('track');track.kind='subtitles';track.label=c.track;track.srclang=language;track.src=s.subtitleUrl;track.default=true;video.appendChild(track);track.track.mode='showing'}video.load();generation=s.generation;retries=0}
    if(direct&&s.state==='PREPARING'&&video.readyState>=1)video.currentTime=Math.min(s.startPositionSeconds,Math.max(0,video.duration-.1));
    return changed
  }
  function playFromMac(){if(startingPlayback)return;suppress=false;startingPlayback=true;showBuffering(c.syncing);video.play().catch(function(){startingPlayback=false;autoPlay=false;buffering.hidden=true;hint.textContent=c.canStart})}
  function commitSeek(){clearTimeout(seekTimer);seekTimer=null;if(pendingSeek==null||!state||state.state!=='PHONE_ACTIVE')return;if(seeking){seekTimer=setTimeout(commitSeek,100);return}var target=pendingSeek;pendingSeek=null;target=Math.max(0,Math.min(state.durationSeconds||0,target));var wasPlaying=!video.paused;seeking=true;resumeAfterSeek=wasPlaying;pauseQuietly();showBuffering(c.seeking);send('seek',target,!wasPlaying).then(function(r){if(!r.ok)throw Error();seeking=false;refresh();if(pendingSeek!=null)seekTimer=setTimeout(commitSeek,100)}).catch(function(){seeking=false;pendingSeek=null;resumeAfterSeek=false;buffering.hidden=true;hint.textContent=c.seekFailed})}
  function queueSeek(){if(!state||state.state!=='PHONE_ACTIVE'||direct)return;pendingSeek=absolutePosition();clearTimeout(seekTimer);seekTimer=setTimeout(commitSeek,160)}
  function recoverAfterPause(){var position=absolutePosition(),source=state.mediaUrl;recovering=true;pausedAt=0;send('play',position,false);video.dataset.url=source;video.src=source+(source.indexOf('?')<0?'?':'&')+'resume='+Date.now();video.load();suppress=false;video.play().catch(function(){recovering=false;buffering.hidden=true;hint.textContent=c.playFailed})}
  function refresh(){
    if(closed)return;
    fetch('state',{cache:'no-store'}).then(function(r){if(r.status===404||r.status===410){showClosed();return null}if(!r.ok)throw Error();return r.json()}).then(function(s){
      if(!s||s.revision<revision)return;revision=s.revision;var previous=state&&state.state;state=s;setTitle(s.title);
      if(!connectedSent){connectedSent=true;send('connected',0,true).catch(function(){connectedSent=false})}
      var changed=loadMedia(s),warning=warningText(s.warning);if(video.textTracks[0])video.textTracks[0].mode='showing';
      if(s.state==='MAC_ACTIVE'){pauseQuietly();clearInterval(heartbeat);started=false;activated=false;startingPlayback=false;autoPlay=false;returningSent=false;buffering.hidden=true;end.disabled=false;status.textContent=c.connected;hint.textContent=c.macPlaying}
      else if(s.state==='PREPARING'){if(previous==='MAC_ACTIVE'){pauseQuietly();clearInterval(heartbeat);started=false;activated=false;autoPlay=true}end.disabled=false;status.textContent=c.connected;hint.textContent=autoPlay?c.syncing:c.canStart;if(autoPlay)playFromMac()}
      else if(s.state==='PHONE_ACTIVE'){status.textContent=c.connected;hint.textContent=s.paused?c.paused:c.playing;end.disabled=false;if(changed&&resumeAfterSeek){resumeAfterSeek=false;suppress=false;video.play().catch(function(){buffering.hidden=true;hint.textContent=c.playFailed})}else if(changed)buffering.hidden=true}
      else if(s.state==='RETURNING'){pauseQuietly();clearInterval(heartbeat);buffering.hidden=true;end.disabled=true;status.textContent=c.connected;hint.textContent=c.syncing;if(!returningSent){returningSent=true;send('return_to_mac',absolutePosition(),true).catch(function(){returningSent=false})}}
      else if(s.state==='ERROR'){pauseQuietly();clearInterval(heartbeat);status.textContent=c.connected;hint.textContent=c.processingFailed;buffering.hidden=true;end.disabled=false}
      else if(s.state==='CLOSED')showClosed();
      if(warning&&s.state!=='ERROR'&&s.state!=='CLOSED')hint.textContent=warning
    }).catch(function(){if(closed)return;status.textContent=c.lost;hint.textContent=c.wifi;buffering.hidden=true})
  }
  video.addEventListener('loadedmetadata',function(){if(direct&&!started&&state)video.currentTime=Math.min(state.startPositionSeconds,Math.max(0,video.duration-.1))});
  video.addEventListener('canplay',function(){retries=0;if(autoPlay&&state&&state.state==='PREPARING')playFromMac();if(!startingPlayback&&!resumeAfterSeek)buffering.hidden=true});
  video.addEventListener('playing',function(){started=true;startingPlayback=false;autoPlay=false;buffering.hidden=true;hint.textContent=c.playing});
  video.addEventListener('waiting',function(){if(startingPlayback||(started&&!video.paused))showBuffering(c.buffering)});
  video.addEventListener('stalled',function(){if(startingPlayback||(started&&!video.paused))showBuffering(c.slow)});
  video.addEventListener('error',function(){if(state&&state.state==='PREPARING'&&retries<4){retries++;showBuffering(c.preparing);setTimeout(function(){video.dataset.url='';refresh()},400*retries);return}buffering.hidden=true;hint.textContent=c.loadFailed});
  video.addEventListener('seeking',function(){if(started&&!direct)queueSeek()});
  video.addEventListener('seeked',function(){if(!started||!state||state.state!=='PHONE_ACTIVE')return;if(direct)send('heartbeat');else queueSeek()});
  video.addEventListener('play',function(){
    if(suppress)return;
    if(state&&state.state==='MAC_ACTIVE'){pauseQuietly();hint.textContent=c.syncing;send('request_to_phone',0,true).then(function(r){if(!r.ok)throw Error()}).catch(function(){hint.textContent=c.playFailed});return}
    if(!state||(state.state!=='PREPARING'&&state.state!=='PHONE_ACTIVE')){pauseQuietly();return}
    if(state.state==='PHONE_ACTIVE'&&!direct&&!recovering&&pausedAt&&Date.now()-pausedAt>3000){recoverAfterPause();return}
    recovering=false;pausedAt=0;
    started=true;startingPlayback=false;autoPlay=false;status.textContent=c.connected;hint.textContent=c.playing;startHeartbeat();
    if(state.state==='PREPARING'&&!activated){activated=true;send('ready',null,false).then(function(r){if(!r.ok)throw Error()}).catch(function(){activated=false;pauseQuietly();hint.textContent=c.playFailed})}else if(state.state==='PHONE_ACTIVE')send('play',null,false)
  });
  video.addEventListener('pause',function(){if(started&&!suppress&&state&&state.state==='PHONE_ACTIVE'){pausedAt=Date.now();status.textContent=c.connected;hint.textContent=c.paused;send('pause',null,true)}});
  end.onclick=function(){end.disabled=true;var p=absolutePosition();send('end',p,true).then(function(r){if(!r.ok)throw Error();pauseQuietly();closed=true;clearInterval(heartbeat);clearInterval(refreshTimer);status.textContent=c.closed;hint.textContent=c.ended}).catch(function(){end.disabled=false;status.textContent=c.lost;hint.textContent=c.wifi})};
  addEventListener('resize',updateTitle);addEventListener('pagehide',function(){if(started&&!closed){var p=absolutePosition();pauseQuietly();navigator.sendBeacon('event',new Blob([JSON.stringify({type:'pause',positionSeconds:p,paused:true})],{type:'application/json'}))}});
  setTitle(c.defaultTitle);refresh();refreshTimer=setInterval(refresh,500);
})();
