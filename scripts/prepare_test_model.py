"""Convert Karpathy's public llama2.c float32 checkpoint and tokenizer to GGUF."""
import argparse
import hashlib
import io
import urllib.request
import zipfile
import struct
from pathlib import Path
import numpy as np
import gguf
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, required=True)
args=parser.parse_args()
url='https://github.com/yeokm1/dosllam2/releases/download/v0.2/dosllam2-v0.2.zip'
with urllib.request.urlopen(url,timeout=120) as response:
 archive=response.read(80 * 1024 * 1024 + 1)
if hashlib.sha256(archive).hexdigest()!='7c9f6ac7070c16df73c7f808317366719cf90afee6208eca9a374978034d04a5':
 raise ValueError('Test fixture archive checksum mismatch')
with zipfile.ZipFile(io.BytesIO(archive)) as z:
 raw=z.read('st-15M.bin')
 tok=z.read('tokenize.bin')
if hashlib.sha256(raw).hexdigest()!='cd590644d963867a2b6e5a1107f51fad663c41d79c149fbecbbb1f95fa81f49a':
 raise ValueError('Checkpoint checksum mismatch')
args.output.parent.mkdir(parents=True,exist_ok=True)
dim,hidden,layers,heads,kvheads,vocab,context=struct.unpack_from('<7i',raw)
shared=vocab>0;vocab=abs(vocab);head_dim=dim//heads;kv_dim=head_dim*kvheads
print('Model',dim,hidden,layers,heads,kvheads,vocab,context)
weights=np.frombuffer(raw,dtype='<f4',offset=28);offset=0
writer=gguf.GGUFWriter(str(args.output),'llama')
writer.add_name('Karpathy TinyStories 15M (integration fixture)')
writer.add_context_length(context);writer.add_embedding_length(dim);writer.add_block_count(layers)
writer.add_feed_forward_length(hidden);writer.add_head_count(heads);writer.add_head_count_kv(kvheads)
writer.add_rope_dimension_count(head_dim);writer.add_layer_norm_rms_eps(1e-5)
writer.add_file_type(gguf.LlamaFileType.ALL_F32)

def take(shape):
 global offset
 count=int(np.prod(shape));v=weights[offset:offset+count].reshape(shape);offset+=count;return v

def tensor(name,shape):writer.add_tensor(name,take(shape))
tensor('token_embd.weight',(vocab,dim))
for i in range(layers):tensor(f'blk.{i}.attn_norm.weight',(dim,))
for i in range(layers):tensor(f'blk.{i}.attn_q.weight',(dim,dim))
for i in range(layers):tensor(f'blk.{i}.attn_k.weight',(kv_dim,dim))
for i in range(layers):tensor(f'blk.{i}.attn_v.weight',(kv_dim,dim))
for i in range(layers):tensor(f'blk.{i}.attn_output.weight',(dim,dim))
for i in range(layers):tensor(f'blk.{i}.ffn_norm.weight',(dim,))
for i in range(layers):tensor(f'blk.{i}.ffn_gate.weight',(hidden,dim))
for i in range(layers):tensor(f'blk.{i}.ffn_down.weight',(dim,hidden))
for i in range(layers):tensor(f'blk.{i}.ffn_up.weight',(hidden,dim))
tensor('output_norm.weight',(dim,))
# Legacy checkpoint stores cosine/sine tables; llama.cpp computes RoPE itself.
take((context,head_dim//2));take((context,head_dim//2))
if not shared:tensor('output.weight',(vocab,dim))
assert offset==len(weights),(offset,len(weights))
pos=4;tokens=[];scores=[];types=[]
for i in range(vocab):
 score,size=struct.unpack_from('<fi',tok,pos);pos+=8;token=tok[pos:pos+size];pos+=size
 if i==0:token=b'<unk>';kind=gguf.TokenType.UNKNOWN
 elif i==1:token=b'<s>';kind=gguf.TokenType.CONTROL
 elif i==2:token=b'</s>';kind=gguf.TokenType.CONTROL
 elif 3<=i<=258:kind=gguf.TokenType.BYTE
 else:token=token.replace(b' ', '\u2581'.encode());kind=gguf.TokenType.NORMAL
 tokens.append(token);scores.append(score);types.append(kind)
assert pos==len(tok)
writer.add_tokenizer_model('llama');writer.add_token_list(tokens);writer.add_token_scores(scores);writer.add_token_types(types)
writer.add_bos_token_id(1);writer.add_eos_token_id(2);writer.add_unk_token_id(0);writer.add_add_bos_token(True)
writer.add_chat_template("{% for message in messages %}{{ message['content'] + '\\n' }}{% endfor %}")
writer.write_header_to_file();writer.write_kv_data_to_file();writer.write_tensors_to_file();writer.close()
print('Converted test checkpoint to',args.output)
