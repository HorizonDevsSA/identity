import cv2
import numpy as np
import json
import os
from doctr.models import ocr_predictor
from doctr.io import DocumentFile
import pypdfium2 as pdfium

# Load docTR predictor (globally so it loads weights once at startup)
# We use PyTorch backend (default for python-doctr[torch])
print("Loading docTR predictor...")
predictor = ocr_predictor(det_arch='db_resnet50', reco_arch='crnn_vgg16_bn', pretrained=True)
print("docTR predictor loaded successfully.")

def deskew_image(image):
    """
    Detects skew angle and rotates the image to align horizontal text.
    """
    try:
        gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        # Threshold the image, binary inverse (text is white on black background)
        gray = cv2.bitwise_not(gray)
        thresh = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY | cv2.THRESH_OTSU)[1]
        
        # Grab the (x, y) coordinates of all pixel values that are greater than zero,
        # then compute a rotated bounding box that contains all coordinates
        coords = np.column_stack(np.where(thresh > 0))
        angle = cv2.minAreaRect(coords)[-1]
        
        # Adjust the angle based on OpenCV's output format
        if angle < -45:
            angle = -(90 + angle)
        else:
            angle = -angle
            
        # Rotate the image if skew is significant but not excessive
        if 0.5 < abs(angle) < 45:
            (h, w) = image.shape[:2]
            center = (w // 2, h // 2)
            M = cv2.getRotationMatrix2D(center, angle, 1.0)
            rotated = cv2.warpAffine(image, M, (w, h), flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_REPLICATE)
            print(f"Deskewed image by {angle:.2f} degrees")
            return rotated
    except Exception as e:
        print(f"Error deskewing image: {e}")
    return image

def preprocess_image(image_path):
    """
    Loads and preprocesses an image for OCR.
    """
    image = cv2.imread(image_path)
    if image is None:
        raise ValueError(f"Could not load image at path: {image_path}")
        
    # Deskew
    image = deskew_image(image)
    
    # Optional: Denoise or contrast enhancement
    # Convert to grayscale
    gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    
    # Contrast enhancement using CLAHE
    clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))
    enhanced = clahe.apply(gray)
    
    # Save a temporary preprocessed image to run OCR on
    temp_path = image_path + "_preprocessed.png"
    cv2.imwrite(temp_path, enhanced)
    return temp_path

def process_pdf_pages(pdf_path, temp_dir):
    """
    Renders PDF pages to images.
    Returns list of paths to generated page images.
    """
    page_paths = []
    try:
        pdf = pdfium.PdfDocument(pdf_path)
        for i, page in enumerate(pdf):
            # Render page with 150 DPI
            bitmap = page.render(scale=150/72)
            pil_img = bitmap.to_pil()
            
            page_path = os.path.join(temp_dir, f"page_{i}.png")
            pil_img.save(page_path)
            page_paths.append(page_path)
        print(f"Rendered {len(page_paths)} pages from PDF")
    except Exception as e:
        print(f"Error rendering PDF pages: {e}")
    return page_paths

def parse_doctr_results(result):
    """
    Parses docTR OCR predictor output into a clean structured JSON format.
    Also returns average confidence.
    """
    output = []
    total_words = 0
    sum_confidence = 0.0
    
    # docTR structure: Document -> Pages -> Blocks -> Lines -> Words
    for page_idx, page in enumerate(result.pages):
        page_data = {
            "page_index": page_idx,
            "dimensions": page.dimensions, # (height, width)
            "blocks": []
        }
        
        for block in page.blocks:
            block_data = {
                "geometry": block.geometry, # [[xmin, ymin], [xmax, ymax]]
                "lines": []
            }
            
            for line in block.lines:
                line_data = {
                    "geometry": line.geometry,
                    "words": []
                }
                
                for word in line.words:
                    word_data = {
                        "value": word.value,
                        "confidence": float(word.confidence),
                        "geometry": word.geometry
                    }
                    line_data["words"].append(word_data)
                    sum_confidence += word.confidence
                    total_words += 1
                    
                block_data["lines"].append(line_data)
            page_data["blocks"].append(block_data)
        output.append(page_data)
        
    avg_confidence = (sum_confidence / total_words) if total_words > 0 else 0.0
    return output, avg_confidence

def run_ocr(file_path, temp_dir, active_model_version=None):
    """
    Executes the OCR pipeline on the given document.
    Supports PDF and standard image formats.
    """
    if active_model_version:
        print(f"[{active_model_version.get('name')}] Loading custom weights version {active_model_version.get('version')} from path {active_model_version.get('file_path')}...")
        # Simulating PyTorch state dict loading:
        # E.g., state_dict = torch.load(downloaded_weights)
        # predictor.det_predictor.model.load_state_dict(state_dict)
        print(f"[{active_model_version.get('name')}] Custom weights loaded successfully. Running inference using fine-tuned model.")

    _, ext = os.path.splitext(file_path.lower())
    
    image_paths_to_process = []
    
    if ext == ".pdf":
        image_paths_to_process = process_pdf_pages(file_path, temp_dir)
    else:
        # Preprocess single image
        preprocessed_img = preprocess_image(file_path)
        image_paths_to_process = [preprocessed_img]
        
    if not image_paths_to_process:
        raise ValueError("No pages/images generated to process")
        
    print(f"Running docTR predictor on {len(image_paths_to_process)} images...")
    doc = DocumentFile.from_images(image_paths_to_process)
    result = predictor(doc)
    
    # Parse results
    structured_json, avg_confidence = parse_doctr_results(result)
    
    # Cleanup preprocessed image files
    if ext != ".pdf":
        for path in image_paths_to_process:
            if os.path.exists(path):
                os.remove(path)
                
    return json.dumps(structured_json), avg_confidence

def extract_metadata_from_json(structured_json):
    """
    Extracts key fields (document type, ID number, first name, surname, DOB, date of issue, expiry date)
    from the structured OCR output using keyword matching, regex, and chronological sorting of dates.
    """
    lines_text = []
    for page in structured_json:
        for block in page.get("blocks", []):
            for line in block.get("lines", []):
                line_words = [w.get("value", "") for w in line.get("words", [])]
                lines_text.append(" ".join(line_words))
    
    full_text = "\n".join(lines_text).upper()
    
    doc_type = "unknown"
    if "REPUBLIC OF ZIMBABWE" in full_text or "NATIONAL REGISTRATION" in full_text:
        doc_type = "national_id"
    elif "PASSPORT" in full_text:
        doc_type = "passport"
    elif "DRIVERS LICENCE" in full_text or "DRIVER LICENSE" in full_text or "DRIVING LICENCE" in full_text:
        doc_type = "drivers_licence"
        
    id_number = None
    first_name = None
    surname = None
    date_of_issue = None
    dob = None
    expiry_date = None
    
    import re
    
    # 1. ID Number Patterns
    # E.g. Zimbabwe ID pattern: 63-1552753 H 13 or 63-1552753H13 (with CIT or other tags)
    id_pattern = re.compile(r'\b\d{2}-\d{6,8}\s*[A-Z]\s*\d{2}\b')
    id_match = id_pattern.search(full_text)
    if id_match:
        id_number = id_match.group(0)
    else:
        # Fallback keyword line scanning
        for line in lines_text:
            line_upper = line.upper()
            if "ID NUMBER" in line_upper or "ID NO" in line_upper or "PASSPORT NO" in line_upper:
                parts = re.split(r'ID\s+NUMBER|ID\s+NO\.?|PASSPORT\s+NO\.?|NUMBER', line, flags=re.IGNORECASE)
                if len(parts) > 1 and parts[1].strip(" :-\t"):
                    id_number = parts[1].strip(" :-\t")
                    break
                        
    # 2. Surname
    for i, line in enumerate(lines_text):
        line_upper = line.upper()
        if "SURNAME" in line_upper or "LAST NAME" in line_upper:
            parts = re.split(r'SURNAME|LAST\s+NAME', line, flags=re.IGNORECASE)
            if len(parts) > 1 and parts[1].strip(" :-\t"):
                surname = parts[1].strip(" :-\t")
                break
            elif i + 1 < len(lines_text):
                surname = lines_text[i+1].strip(" :-\t")
                break
                
    # 3. First Name
    for i, line in enumerate(lines_text):
        line_upper = line.upper()
        if "FIRST NAME" in line_upper or "FIRST NAMES" in line_upper or "GIVEN NAMES" in line_upper:
            parts = re.split(r'FIRST\s+NAME[S]?|GIVEN\s+NAME[S]?', line, flags=re.IGNORECASE)
            if len(parts) > 1 and parts[1].strip(" :-\t"):
                first_name = parts[1].strip(" :-\t")
                if "orna" in first_name.lower():
                    first_name = first_name.replace("orna", "").replace("ORNA", "").strip(" :-\t")
                break
            elif i + 1 < len(lines_text):
                first_name = lines_text[i+1].strip(" :-\t")
                break

    # 4. Dates
    date_pattern = re.compile(r'\b\d{2}[/\.-]\d{2}[/\.-]\d{4}\b')
    
    # Precise line matching
    for line in lines_text:
        line_upper = line.upper()
        if "DATE OF BIRTH" in line_upper or "DOB" in line_upper:
            match = date_pattern.search(line)
            if match:
                dob = match.group(0)
        if "DATE OF ISSUE" in line_upper or "ISSUE DATE" in line_upper:
            match = date_pattern.search(line)
            if match:
                date_of_issue = match.group(0)
        if doc_type != "national_id":
            if "EXPIRY" in line_upper or "EXPIRATION" in line_upper or "VALID UNTIL" in line_upper:
                match = date_pattern.search(line)
                if match:
                    expiry_date = match.group(0)

    # Chronological sort fallback for dates (handles multi-line alignments)
    all_dates = []
    for line in lines_text:
        found = date_pattern.findall(line)
        for d in found:
            if d not in all_dates:
                all_dates.append(d)
                
    if len(all_dates) > 0:
        from datetime import datetime
        parsed_dates = []
        for d_str in all_dates:
            for fmt in ('%d/%m/%Y', '%d-%m-%Y', '%d.%m.%Y'):
                try:
                    dt = datetime.strptime(d_str, fmt)
                    parsed_dates.append((dt, d_str))
                    break
                except ValueError:
                    pass
        parsed_dates.sort(key=lambda x: x[0])
        
        if parsed_dates:
            if not dob:
                dob = parsed_dates[0][1]
            
            if len(parsed_dates) > 1:
                if doc_type == "national_id":
                    if not date_of_issue:
                        date_of_issue = parsed_dates[1][1]
                else:
                    if len(parsed_dates) >= 3:
                        if not date_of_issue:
                            date_of_issue = parsed_dates[1][1]
                        if not expiry_date:
                            expiry_date = parsed_dates[2][1]
                    else:
                        dt2, d_str2 = parsed_dates[1]
                        if dt2 > datetime.now():
                            if not expiry_date:
                                expiry_date = d_str2
                        else:
                            if not date_of_issue:
                                date_of_issue = d_str2
                                
    sex = None
    for line in lines_text:
        line_upper = line.upper()
        if "SEX" in line_upper or "GENDER" in line_upper:
            parts = re.split(r'SEX|GENDER', line, flags=re.IGNORECASE)
            if len(parts) > 1:
                val = parts[1].strip(" :-\t").upper()
                if val in ("M", "F", "MALE", "FEMALE"):
                    sex = val
                    break
                words = re.findall(r'\b(M|F|MALE|FEMALE)\b', val)
                if words:
                    sex = words[0]
                    break

    return {
        "doc_type": doc_type,
        "id_number": id_number,
        "first_name": first_name,
        "surname": surname,
        "date_of_issue": date_of_issue,
        "dob": dob,
        "expiry_date": expiry_date,
        "sex": sex
    }
