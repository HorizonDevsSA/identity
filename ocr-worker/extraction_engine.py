import re
import json

def get_word_center(word):
    # docTR coordinates are: [[ymin, xmin], [ymax, xmax]]
    geom = word.get("geometry", [[0, 0], [0, 0]])
    ymin, xmin = geom[0]
    ymax, xmax = geom[1]
    return (xmin + xmax) / 2.0, (ymin + ymax) / 2.0

def get_word_bbox(word):
    geom = word.get("geometry", [[0, 0], [0, 0]])
    return geom[0][0], geom[0][1], geom[1][0], geom[1][1] # ymin, xmin, ymax, xmax

def extract_fields_from_ocr(raw_json_str, schema_fields):
    """
    Given a docTR parsed raw JSON string and a list of schema fields,
    performs spatial proximity key-value extraction.
    
    schema_fields format:
    [
        {
            "key": "invoice_number",
            "keywords": ["invoice no", "invoice #", "inv no", "number"],
            "regex": "INV-\\d+" # optional
        }
    ]
    """
    try:
        pages = json.loads(raw_json_str)
    except Exception as e:
        print(f"Error parsing raw OCR JSON: {e}")
        return []

    extracted_results = []

    # Process page by page
    for page in pages:
        # 1. Gather all words from all lines/blocks on the page with absolute/relative coordinates
        flat_words = []
        flat_lines = []
        
        for block in page.get("blocks", []):
            for line in block.get("lines", []):
                line_words = []
                for w in line.get("words", []):
                    word_val = w.get("value", "")
                    word_conf = w.get("confidence", 1.0)
                    ymin, xmin, ymax, xmax = get_word_bbox(w)
                    word_obj = {
                        "value": word_val,
                        "confidence": word_conf,
                        "ymin": ymin,
                        "xmin": xmin,
                        "ymax": ymax,
                        "xmax": xmax
                    }
                    flat_words.append(word_obj)
                    line_words.append(word_obj)
                
                if line_words:
                    line_words.sort(key=lambda x: x["xmin"])
                    line_text = " ".join([w["value"] for w in line_words])
                    line_ymin = min([w["ymin"] for w in line_words])
                    line_xmin = min([w["xmin"] for w in line_words])
                    line_ymax = max([w["ymax"] for w in line_words])
                    line_xmax = max([w["xmax"] for w in line_words])
                    
                    flat_lines.append({
                        "text": line_text,
                        "words": line_words,
                        "ymin": line_ymin,
                        "xmin": line_xmin,
                        "ymax": line_ymax,
                        "xmax": line_xmax
                    })

        # 2. Iterate through each field in the schema
        for field in schema_fields:
            key = field.get("key")
            keywords = field.get("keywords", [key])
            regex_str = field.get("regex", "")
            
            best_candidate = None
            best_score = -1.0
            
            # Search for keyword matches in flat lines
            for line_idx, line in enumerate(flat_lines):
                line_text_lower = line["text"].lower()
                
                # Check each keyword
                for keyword in keywords:
                    kw_lower = keyword.lower()
                    if kw_lower in line_text_lower:
                        # Find the matching start word index
                        kw_words = kw_lower.split()
                        words_in_line = [w["value"].lower() for w in line["words"]]
                        
                        # Find subsequence index
                        sub_start = -1
                        for idx in range(len(words_in_line) - len(kw_words) + 1):
                            match = True
                            for k_i, kw_w in enumerate(kw_words):
                                # Clean words of punctuation for matching
                                w_clean = re.sub(r'[^\w]', '', words_in_line[idx + k_i])
                                kw_clean = re.sub(r'[^\w]', '', kw_w)
                                if kw_clean not in w_clean and w_clean not in kw_clean:
                                    match = False
                                    break
                            if match:
                                sub_start = idx
                                break
                        
                        if sub_start != -1:
                            # We found the keyword bounding box
                            matched_words = line["words"][sub_start:sub_start + len(kw_words)]
                            kw_xmin = min([w["xmin"] for w in matched_words])
                            kw_xmax = max([w["xmax"] for w in matched_words])
                            kw_ymin = min([w["ymin"] for w in matched_words])
                            kw_ymax = max([w["ymax"] for w in matched_words])
                            
                            # Candidate 1: Same line, to the right of the keyword
                            right_words = line["words"][sub_start + len(kw_words):]
                            if right_words:
                                val_str = " ".join([w["value"] for w in right_words]).strip(" :-,\t")
                                if val_str:
                                    conf = sum([w["confidence"] for w in right_words]) / len(right_words)
                                    val_xmin = min([w["xmin"] for w in right_words])
                                    val_xmax = max([w["xmax"] for w in right_words])
                                    val_ymin = min([w["ymin"] for w in right_words])
                                    val_ymax = max([w["ymax"] for w in right_words])
                                    
                                    # Validate regex if present
                                    is_match = True
                                    regex_boost = 0.0
                                    if regex_str:
                                        match_obj = re.search(regex_str, val_str)
                                        if match_obj:
                                            # If there's a match group, pull it, otherwise full match
                                            val_str = match_obj.group(0)
                                            regex_boost = 0.2
                                        else:
                                            is_match = False
                                    
                                    if is_match:
                                        dist = val_xmin - kw_xmax
                                        proximity_score = max(0.0, 1.0 - (dist * 2.0)) # Closeness multiplier
                                        score = (conf * 0.6) + (proximity_score * 0.2) + regex_boost
                                        
                                        if score > best_score:
                                            best_score = score
                                            best_candidate = {
                                                "key": key,
                                                "value": val_str,
                                                "confidence": min(1.0, float(score)),
                                                "bounding_box": json.dumps([[val_ymin, val_xmin], [val_ymax, val_xmax]])
                                            }
                            
                            # Candidate 2: In the lines below (vertical proximity)
                            # Scan lines directly below this one
                            for next_idx in range(line_idx + 1, min(line_idx + 4, len(flat_lines))):
                                next_line = flat_lines[next_idx]
                                # Check horizontal alignment: center of next line is close to center of keyword or within bounds
                                kw_center_x = (kw_xmin + kw_xmax) / 2.0
                                next_line_center_x = (next_line["xmin"] + next_line["xmax"]) / 2.0
                                
                                # If the horizontal centers align or next line overlaps kw x-range
                                if abs(next_line_center_x - kw_center_x) < 0.25 or (next_line["xmin"] <= kw_center_x <= next_line["xmax"]):
                                    val_str = next_line["text"].strip(" :-,\t")
                                    if val_str:
                                        conf = sum([w["confidence"] for w in next_line["words"]]) / len(next_line["words"])
                                        
                                        is_match = True
                                        regex_boost = 0.0
                                        if regex_str:
                                            match_obj = re.search(regex_str, val_str)
                                            if match_obj:
                                                val_str = match_obj.group(0)
                                                regex_boost = 0.2
                                            else:
                                                is_match = False
                                                
                                        if is_match:
                                            dist_y = next_line["ymin"] - kw_ymax
                                            proximity_score = max(0.0, 1.0 - (dist_y * 3.0)) # Vertical distance penalty is higher
                                            score = (conf * 0.5) + (proximity_score * 0.2) + regex_boost
                                            
                                            if score > best_score:
                                                best_score = score
                                                best_candidate = {
                                                    "key": key,
                                                    "value": val_str,
                                                    "confidence": min(1.0, float(score)),
                                                    "bounding_box": json.dumps([[next_line["ymin"], next_line["xmin"]], [next_line["ymax"], next_line["xmax"]]])
                                                }
            
            # If a candidate was found, add to results
            if best_candidate:
                extracted_results.append(best_candidate)
                
        # If we have extracted results on this page, stop or merge (normally one template matches)
        if extracted_results:
            break

    return extracted_results
